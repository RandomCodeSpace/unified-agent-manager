# uam: Publishing a page (uam_publish)

Use `uam_publish` when you built a page or small site for the owner to open
and come back to: a report, a prototype, a dashboard. To point the owner at a
file you are still working on, or any other file, write its path or use
`uam_show_file` instead (see `files.md`): that shows the file as it is now.

- Pass `path`, a folder with `index.html` at its top or one file, and a
  `title`. uam copies a snapshot of it, so your later edits do not change
  what was published. The owner gets a card in this conversation to open it,
  and the Thread's artifacts list.
- Publish again with the same `title`, or with the `id` the result gave you,
  to add a new version. The owner can open older versions; uam keeps an
  artifact's last 10 versions and at most 20 artifacts per Thread.
- The snapshot leaves out `.git`, `node_modules`, `.env` files, symbolic
  links, hard-linked files and special files (the result names the links and
  special files it left out), and holds at most 500 files and folders, 32
  folders deep, and 25 MiB. A `path` inside `.git`, `node_modules` or a
  `.env` folder, or through a symbolic link, is refused. Publish the built
  page's own folder (`dist/`, `site/`), not the repository.
- A published page reaches only its own files. Link them with relative
  paths (`style.css`, `img/logo.png`, `about/`); a root-relative `/assets/…`
  path does not resolve. Build tools need a relative base (Vite
  `base: './'`).
- The page runs in a sandbox with an opaque origin. Use classic scripts
  (`<script src="app.js">`) and inline data. ES modules
  (`<script type="module">`), `fetch` or XHR of its own files,
  `localStorage`, `sessionStorage`, IndexedDB, `history.pushState`, calls
  to other hosts and scripts, styles, images or fonts from other hosts do
  not work. The page's own style sheets, images and fonts load, as do
  inline scripts and styles and `data:` URLs.
