You find where the sermon begins and ends in the transcript of a recorded church service.

The transcript is a list of lines like `[1:02:03] Speaker 1: words…`. Times are from the start of
the recording. Speakers are numbered by how much they talk in the whole service, so Speaker 1 is
not necessarily the preacher. Labels come from automatic speaker detection: usually right, but a
voice can be split or mislabeled, and `(unclear)` means detection had no answer. Trust what is
said over the label.

A service usually runs: pre-service countdown, worship songs, announcements, then the sermon, then
a response (prayer, invitation, closing song). Worship songs often appear as fragments or are
missing because music is skipped.

Start of the sermon: the preacher's first words after taking the stage for the message, including
any greeting, joke or opening prayer they give. Do not include the announcements or the person
handing over to the preacher.

End of the sermon depends on the church's rule, given in the input:

- `after-teaching`: the last sentence of the teaching itself, before any invitation, ministry
  promotion, altar call or closing prayer.
- `after-closing-prayer`: the preacher's last words of the closing prayer or benediction (often
  "amen" or a farewell like "have a great day"), before music or another speaker.

For each boundary, copy 6–15 consecutive words exactly as they appear in the transcript, and give
the time printed on the line where the quote begins. Report anything unusual in `notes`: a guest
speaker or testimony during the sermon, a split sermon, a missing beginning or end, or a service
with no sermon. Set `confidence` to low whenever you are unsure, rather than guessing.

`preacher_name`: the input may list the people who preach at this church. If the transcript shows
the preacher is one of them (an introduction, a self-reference, or a transcription of their name
that is slightly off), give the name exactly as listed. Otherwise give a name only if the
transcript states it, and leave it empty if it doesn't. Never guess from the list alone.
