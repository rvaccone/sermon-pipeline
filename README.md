# sermon-pipeline

> **Status: prototype.** I'm building this for Feather Sound Church in Clearwater, FL, to show a
> better way to produce each week's sermon video, podcast and clips. It has been tested on one
> service so far, runs only on Apple Silicon Macs, and comes without support. Other churches are
> welcome to try it.

Turns a recorded church service into publish-ready sermon outputs, all on a Mac:

- the sermon cut from the service, encoded for YouTube, with loudness-normalized audio
- YouTube captions (`.srt`) and a readable transcript
- podcast audio (MP3) at podcast loudness
- YouTube title options, a description with chapters, tags and hashtags, and a podcast description
- three vertical clips that keep the preacher centered, with word-by-word captions
- four thumbnail options
- `Review.html`, one page showing all of it, with anything doubtful flagged at the top

These are the files the church would actually publish. For now the pipeline stops at a folder of
them, and a person reviews and uploads them; the goal is for it to publish on its own once its
outputs have proven reliable. If it isn't used one week, the church's usual process still works
(see [docs/fallback.md](docs/fallback.md)).

## Setup

```sh
nix develop            # pinned toolchain: Go, ffmpeg-full, whisper.cpp, sherpa-onnx, ImageMagick
make build             # bin/sermon and bin/sermon-vision (the Swift helper uses Xcode's toolchain)
cp config.example.toml config.toml   # then replace the church's details with yours
bin/sermon setup       # downloads the AI models (~3.3 GB) to ~/.cache/sermon-pipeline/models
```

The steps that read the transcript use Claude through the Claude Code CLI in headless mode
(`claude -p`), on your Claude subscription; no API key is needed. Sign in to `claude` once. Each
request runs isolated: no tools, no MCP servers, no settings or saved session, in an empty
temporary folder, so it can only read the transcript and answer.

## Use

```sh
bin/sermon run ~/Downloads/service.mp4 --date 2026-07-26
```

Each sermon gets its own folder in `~/Sermons`, named by date, title and passage:

```
~/Sermons/
  2026-07-26 · Judging Without Hypocrisy (Matthew 7.1-6)/
    Review.html              start here: everything below, with anything to check flagged
    Transcript.txt
    YouTube/
      Sermon.mp4             upload this
      Captions.srt           upload as the video's subtitles
      Title options.txt
      Description.txt        includes chapters and hashtags
      Tags.txt               paste into the Tags field
      Thumbnails/1.jpg … 4.jpg
    Podcast/
      Episode.mp3
      Description.txt
    Clips/
      01-….mp4 …
      Post captions.txt
  .work/                     the pipeline's own files (transcripts, tracking, cache); safe to ignore
```

The output folder holds hard links into `.work`, so the videos take no extra space. Files you add
to an output folder yourself are left alone.

Common options:

| Option                            | Use                                                                                     |
| --------------------------------- | --------------------------------------------------------------------------------------- |
| `--start 24:41 --end 1:22:24`     | The sermon's cut points are wrong: set them yourself. Everything downstream re-renders. |
| `--preacher "Pastor Art Dykstra"` | Name the preacher in descriptions when the transcript doesn't.                          |
| `--only video,podcast`            | Run just these stages (and what they need).                                             |
| `--force describe`                | Ask Claude again even though nothing changed.                                           |

Re-running is cheap: a stage is reused unless its settings, the files it reads, or the tools and
models have changed. The review page is always rebuilt last and lists anything that failed.

## How it works

```
probe → analysis audio ─┬─ transcribe ─┐
                        └─ diarize ────┴─ sermon ─┬─ corrections ─┬─ captions
                                                  │               ├─ describe ── thumbnails
                                                  │               └─ clip picks ─ clips
                                                  ├─ master audio ─┬─ video
                                                  │                └─ podcast
                                                  └─ thumbnail frames
                                                                     … → review
```

| Stage        | What it does                                                                                                                                                                             | Tool                      |
| ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| transcribe | Silero VAD finds the speech; each stretch is transcribed by Whisper large-v3-turbo with word timing (DTW) and a short punctuated prompt carried through every window, then shifted back onto the recording's timeline; a stretch that loops is transcribed again without its own context, and any loop that remains is flagged | whisper.cpp |
| diarize      | Who speaks when                                                                                                                                                                          | sherpa-onnx               |
| sermon       | Claude proposes start/end **with quotes**; the quotes must exist in the transcript; cuts snap into pauses; speaker detection and length are cross-checked, and disagreements are flagged | Claude, ffmpeg            |
| corrections | Claude proposes fixes for mis-heard glossary terms and Bible references; one is applied only if it sounds like the transcribed words, changes nothing else, and (for a reference) names a real book and numbers that were said | Claude |
| master audio | Rumble filter, gentle compression, gain to −14 LUFS (video) / −16 LUFS (podcast), 4× oversampled limiting; loudness and true peak are verified **after** encoding                        | ffmpeg                    |
| video        | One x264 encode from the source (slow preset, CRF 13, keyframes every 2 s)                                                                                                               | ffmpeg                    |
| clip picks | Claude proposes candidates; each is cut into pauses, then checked against the transcript, length and speaker turns; a second Claude pass reviews exactly what will be rendered | Claude |
| clips | Neck tracking at 30 fps, a continuously centered 4:5 window, word-by-word captions, audio mastered per clip, x264 | Apple Vision, ffmpeg |
| thumbnails   | Face-quality scoring with a penalty for on-screen text, then composition                                                                                                                 | Apple Vision, ImageMagick |

The settings come from probing a real service: x264 beat Apple's hardware encoder on VMAF (worst-1%
frames 96.9 vs 95.4 at half the bitrate); whisper.cpp drops punctuation on long recordings unless the
prompt is carried, and its built-in VAD mode reports word times on a compressed timeline, so speech
regions are found separately; large-v3 fell into repetition loops (one sentence repeated for 18
minutes) where large-v3-turbo transcribed the same audio cleanly with the same punctuation, so turbo is
the default; noise reduction made no audible difference on this church's gated microphone;
a full-height 9:16 crop of 720p video kept the preacher's whole body in frame only 35–65% of the time, while the
4:5 window keeps him whole 89–99% of the time.

On that service the pipeline placed the sermon's start 3.7 s after a hand-picked cut (it starts at the
preacher's first sentence at the lectern rather than the "Amen" he says while walking up) and its end
within 1 s; the full run took 31 minutes on an M-series Mac, most of it speaker detection and the
video encode.

## Settings

`config.toml` (start from [config.example.toml](config.example.toml), which is Feather Sound's)
holds the church's details and anything that differs from the defaults in
[internal/config/config.go](internal/config/config.go): the glossary, where the sermon ends
(`after-closing-prayer` or `after-teaching`), Claude model and effort, loudness targets, encoder
quality, clip count and length, caption font.

## Measuring sermon detection

List a few past services with the cut points you'd choose, then:

```sh
bin/sermon eval eval/labels.toml
```

It reports how many seconds the automatic cuts are off. Rerun it after changing prompts, models or
settings. See [eval/labels.example.toml](eval/labels.example.toml).

## Development

```sh
make test     # go test ./...
make vet
```

Code layout: `cmd/sermon` is the CLI; `internal/stages` wires the pipeline together; every other
package under `internal/` does one job (`transcript`, `diarize`, `sermon`, `clips`, `media`, …) and
is tested on its own. Claude prompts live in `internal/llm/prompts/` and are versioned by content
hash in `run.json`.

## License

[FSL 1.1 with an MIT future grant](LICENSE.md). Fair Source: use it, read it, modify it for your
church. Do not ship a competing substitute. Each release becomes MIT two years after it ships.
