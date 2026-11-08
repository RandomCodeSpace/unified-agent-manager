# uam: Files: paths, links and uam_show_file

- A Markdown link to a file, or a path in inline code, becomes a **file
  chip** that opens a preview, once uam finds a regular file of that path
  inside the Thread's directory. The chip shows your link text when it says
  more than the path, otherwise the file's name.
- Write paths relative to the Thread's directory (`internal/web/server.go`),
  or absolute. A bare `server.go` for a nested file links only when exactly
  one file of that name is in the Thread's record or directory; with two it
  stays code, so prefer the path. Inline code
  counts when it holds a `/` or ends in an extension (`package.json`);
  globs, flags, `~/` paths and directories stay code. Cite a line as
  `internal/web/server.go:120` (or `:120:5`); the preview opens marked
  there.
- A file you created under the system temp directory (`/tmp/report.html`,
  up to 20 MiB) gets an **Open temp file** button instead. Any other path
  outside the Thread's directory stays plain text.
- The preview shows images, and plays PDFs, audio and video in place. An HTML page runs in a
  sandboxed frame and loads its relative CSS, scripts and images from the
  Thread's directory. Other text shows its first 64 KiB.
- `uam_show_file` checks one file and records it for the owner: `path`
  (required), optional `title` (up to 128 characters) and `type_hint`
  (such as `html`). It never reads the file. A refusal means the path is
  missing or outside the Thread's directory and the temp directory: correct
  it. The call stays folded, so then write the path in your reply as a link
  or inline code.

## Images

- `![alt](path)` shows a png, jpeg, gif or webp image of the Thread's
  directory, up to 20 MiB, inline; the owner taps it to enlarge.
- An SVG, an image under the temp directory or one at a web address shows
  as a link or a note, not inline. To show a picture, write a png into the
  Thread's directory.
