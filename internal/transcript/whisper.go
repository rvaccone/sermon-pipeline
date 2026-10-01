package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rvaccone/sermon-pipeline/internal/command"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// WhisperOptions configures transcription.
type WhisperOptions struct {
	Audio    string // 16 kHz mono WAV
	Model    string // whisper.cpp model file
	VAD      string // Silero VAD model file
	Language string // e.g. "en"
	WorkDir  string // where speech chunks and whisper's output are written
}

// Speech regions closer than chunkGap are transcribed together, so whisper keeps its context
// through a sermon's pauses; longer gaps (music, silence) split the recording into chunks.
const (
	chunkGap = 5.0
	chunkPad = 0.3
)

// Transcribe finds the speech in the recording, transcribes it with whisper.cpp and returns
// word-timed text on the recording's timeline.
//
// whisper.cpp's own --vad mode is not used: in v1.9 it reports word times on the speech-only
// timeline it builds internally, which drifts minutes away from the recording. Instead the
// Silero model finds speech regions here, each chunk is transcribed on its own, and its words are
// shifted back by the chunk's start.
//
// The prompt is a short punctuated example, carried into every 30-second window: without it,
// punctuation and capitals disappear after a few minutes. It deliberately leaves out the
// glossary, which seemed to trigger repetition loops; the corrections stage fixes names instead.
// DTW alignment gives accurate word times (and requires flash attention off).
//
// Whisper can fall into a loop, repeating one sentence for minutes. A chunk that loops is
// transcribed again without conditioning on its own earlier output, which breaks the loop;
// anything still repeated is reported in Repeats.
func Transcribe(ctx context.Context, o WhisperOptions) (Transcript, error) {
	preset, err := dtwPreset(o.Model)
	if err != nil {
		return Transcript{}, err
	}
	regions, err := speechRegions(ctx, o.Audio, o.VAD)
	if err != nil {
		return Transcript{}, err
	}
	chunks := chunk(regions)
	if len(chunks) == 0 {
		return Transcript{}, fmt.Errorf("no speech found in %s", o.Audio)
	}
	files := make([]string, len(chunks))
	for i, c := range chunks {
		files[i] = filepath.Join(o.WorkDir, fmt.Sprintf("speech-%03d.wav", i))
		if err := command.Run(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
			"-ss", timeline.Arg(c.Start), "-t", timeline.Arg(c.Duration()), "-i", o.Audio, "-c:a", "pcm_s16le", files[i]); err != nil {
			return Transcript{}, err
		}
	}
	if err := whisper(ctx, o, preset, false, files...); err != nil {
		return Transcript{}, err
	}

	var all Transcript
	for i, c := range chunks {
		part, err := readChunk(files[i], c)
		if err != nil {
			return Transcript{}, err
		}
		if len(Repeats(part)) > 0 {
			if err := whisper(ctx, o, preset, true, files[i]); err != nil {
				return Transcript{}, err
			}
			if retry, err := readChunk(files[i], c); err == nil && len(Repeats(retry)) < len(Repeats(part)) {
				part = retry
			}
		}
		all.Words = append(all.Words, part...)
		all.Repeats = append(all.Repeats, Repeats(part)...)
	}
	return all, nil
}

// whisper transcribes files in one run, so the model loads once. Each file gets <file>.json.
// isolated stops each window from conditioning on the text before it, which breaks repetition
// loops at some cost to punctuation.
func whisper(ctx context.Context, o WhisperOptions, preset string, isolated bool, files ...string) error {
	args := []string{
		"--model", o.Model,
		"--language", o.Language,
		"--prompt", prompt,
		"--carry-initial-prompt",
		"--dtw", preset,
		"--no-flash-attn",
		"--output-json-full",
		"--no-prints",
	}
	if isolated {
		args = append(args, "--max-context", "0")
	}
	for _, f := range files {
		args = append(args, "--file", f)
	}
	return command.Run(ctx, "whisper-cli", args...)
}

// readChunk parses a chunk's whisper output and shifts its words onto the recording's timeline.
func readChunk(file string, c timeline.Span) ([]Word, error) {
	data, err := os.ReadFile(file + ".json")
	if err != nil {
		return nil, err
	}
	part, err := parseWhisperJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(file), err)
	}
	var words []Word
	for _, w := range part.Words {
		if w.Start > c.Duration() {
			continue // whisper occasionally times a final word past the audio it was given
		}
		w.Start += c.Start
		w.End = math.Min(w.End+c.Start, c.End)
		words = append(words, w)
	}
	return words, nil
}

// Repeats finds stretches where the same sentence (of four or more words) appears three or more
// times in a row: Whisper's repetition loop. Genuine speech almost never does this.
func Repeats(words []Word) []timeline.Span {
	var sentences [][]Word
	start := 0
	for i, w := range words {
		if endsSentence(w.Text) || i == len(words)-1 {
			sentences = append(sentences, words[start:i+1])
			start = i + 1
		}
	}
	var loops []timeline.Span
	for i := 0; i < len(sentences); {
		j := i + 1
		key := normalize(Join(sentences[i]))
		for j < len(sentences) && len(sentences[i]) >= 4 && normalize(Join(sentences[j])) == key {
			j++
		}
		if j-i >= 3 {
			loops = append(loops, timeline.Span{Start: sentences[i][0].Start, End: sentences[j-1][len(sentences[j-1])-1].End})
		}
		i = j
	}
	return loops
}

var segmentLine = regexp.MustCompile(`start = ([0-9.]+), end = ([0-9.]+)`)

// speechRegions runs Silero VAD over the recording. Times are on the recording's timeline.
func speechRegions(ctx context.Context, audio, model string) ([]timeline.Span, error) {
	out, err := command.Output(ctx, "whisper-vad-speech-segments", "--no-prints",
		"--vad-model", model, "--vad-min-silence-duration-ms", "500", "--file", audio)
	if err != nil {
		return nil, err
	}
	var regions []timeline.Span
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if m := segmentLine.FindStringSubmatch(scanner.Text()); m != nil {
			start, _ := strconv.ParseFloat(m[1], 64)
			end, _ := strconv.ParseFloat(m[2], 64)
			regions = append(regions, timeline.Span{Start: start / 100, End: end / 100}) // centiseconds
		}
	}
	return regions, scanner.Err()
}

// chunk merges speech regions separated by less than chunkGap and pads each chunk slightly.
func chunk(regions []timeline.Span) []timeline.Span {
	var chunks []timeline.Span
	for _, r := range regions {
		if n := len(chunks); n > 0 && r.Start-chunks[n-1].End < chunkGap {
			chunks[n-1].End = r.End
			continue
		}
		chunks = append(chunks, r)
	}
	for i := range chunks {
		chunks[i].Start = math.Max(0, chunks[i].Start-chunkPad)
		chunks[i].End += chunkPad
	}
	return chunks
}

// prompt is a short, punctuated example of how the transcript should read.
const prompt = "Good morning, church. Please turn with me to Matthew, chapter 7. Let's pray."

// dtwPreset maps a model file to whisper.cpp's alignment-head preset name.
func dtwPreset(modelFile string) (string, error) {
	name := filepath.Base(modelFile)
	for _, preset := range []struct{ file, name string }{
		{"large-v3-turbo", "large.v3.turbo"},
		{"large-v3", "large.v3"},
		{"large-v2", "large.v2"},
		{"medium.en", "medium.en"},
		{"medium", "medium"},
	} {
		if strings.Contains(name, preset.file) {
			return preset.name, nil
		}
	}
	return "", fmt.Errorf("no word-alignment preset for whisper model %s", name)
}

type whisperJSON struct {
	Transcription []struct {
		Tokens []struct {
			Text    string `json:"text"`
			DTW     int    `json:"t_dtw"` // centiseconds; -1 when unavailable
			Offsets struct {
				From int `json:"from"` // milliseconds
			} `json:"offsets"`
		} `json:"tokens"`
	} `json:"transcription"`
}

// parseWhisperJSON merges whisper's sub-word tokens into words. A token that begins with a space
// starts a new word; anything else continues the previous one. Special tokens like "[_BEG_]"
// are dropped.
func parseWhisperJSON(data []byte) (Transcript, error) {
	var doc whisperJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return Transcript{}, fmt.Errorf("reading whisper.cpp output: %w", err)
	}
	var words []Word
	for _, seg := range doc.Transcription {
		first := true
		for _, tok := range seg.Tokens {
			if strings.HasPrefix(tok.Text, "[_") || strings.TrimSpace(tok.Text) == "" {
				continue
			}
			start := float64(tok.Offsets.From) / 1000
			if tok.DTW >= 0 {
				start = float64(tok.DTW) / 100
			}
			if strings.HasPrefix(tok.Text, " ") || first || len(words) == 0 {
				words = append(words, Word{Text: strings.TrimSpace(tok.Text), Start: start})
			} else {
				words[len(words)-1].Text += tok.Text
			}
			first = false
		}
	}
	words = dropPunctuationOnly(words)
	for i := range words {
		end := words[i].Start + spokenLength(words[i].Text)
		if i+1 < len(words) {
			end = math.Min(end, words[i+1].Start)
		}
		words[i].End = math.Max(words[i].Start, end)
	}
	return Transcript{Words: words}, nil
}

// dropPunctuationOnly removes "words" with no letters or digits (whisper occasionally emits a
// lone "-"), which would otherwise show up in captions.
func dropPunctuationOnly(words []Word) []Word {
	out := words[:0]
	for _, w := range words {
		if strings.IndexFunc(w.Text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			out = append(out, w)
		}
	}
	return out
}

// spokenLength estimates how long a word takes to say, so a pause shows up as a gap between one
// word's end and the next word's start. Conversational speech runs about 15 characters a second.
func spokenLength(word string) float64 {
	return math.Min(1.0, 0.08+0.065*float64(utf8.RuneCountInString(word)))
}
