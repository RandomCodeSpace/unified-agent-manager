import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { compile } from '@tailwindcss/node';

const base = fileURLToPath(new URL('../src', import.meta.url));
const styles = await compile(readFileSync(`${base}/index.css`, 'utf8'), { base, onDependency() {} });
const source = (file) => readFileSync(`${base}/${file}`, 'utf8');

test('the vertical fading rule works under a variant (the thought and step rails draw it on ::before)', () => {
  const css = styles.build(['before:fade-rule-y', 'fade-rule-y']);
  assert.match(css, /\.before\\:fade-rule-y::before\s*\{[^}]*linear-gradient\(to bottom/);
  assert.match(css, /\.fade-rule-y\s*\{[^}]*linear-gradient\(to bottom/);
});

test('the composer toolbar folds (lib/toolbarFold) match the row attribute, and the More fold wins over sm:hidden', () => {
  const css = styles.build(['sm:hidden', 'sm:in-data-[fold~=more]:inline-flex', 'in-data-[fold~=model]:hidden']);
  assert.match(css, /:where\(\[data-fold~="model"\]\) \.in-data-\\\[fold\\~\\=model\\\]\\:hidden\s*\{\s*display: none/);
  // Same specificity, so the later rule decides.
  const hidden = css.indexOf('.sm\\:hidden {');
  const shown = css.indexOf(':where([data-fold~="more"]) .sm\\:in-data-');
  assert.ok(hidden >= 0 && shown > hidden, 'the More fold comes after sm:hidden');
});

test('only the ring, the live line\'s breathing dot and the step dots loop: the stylesheet has no other endless animation', () => {
  const source = readFileSync(`${base}/index.css`, 'utf8');
  const endless = source.split('\n').filter((line) => /\binfinite\b/.test(line));
  assert.deepEqual(endless.map((line) => line.trim()), ['--animate-spin: spin 1s linear infinite;', '--animate-breathe: breathe 2.4s ease-in-out infinite;', '--animate-step: step 1.2s ease-in-out infinite;']);
});

test('a table cell never breaks inside a word, so an identifier stays whole and a wide table scrolls in its box', () => {
  const css = styles.build([]);
  const cells = css.match(/\.data-table :is\(th, td\)[^{]*\{([^}]*)\}/);
  assert.ok(cells, 'the data table cell rule');
  assert.match(cells[1], /overflow-wrap: normal;/);
  assert.doesNotMatch(css.slice(css.indexOf('.data-table')), /\.data-table[^{]*\{[^}]*overflow-wrap: (anywhere|break-word)/);
});

test('a panel head leaves the top pixel row to the surface under it, so a reader keeps its top highlight', () => {
  const head = styles.build([]).match(/\.panel-head\s*\{([^}]*)\}/);
  assert.ok(head, 'the panel head rule');
  assert.match(head[1], /background:\s*linear-gradient\(var\(--color-raised\), var\(--color-raised\)\) 0 1px no-repeat/);
});

test('a disclosure\'s chevron turns with its own details, not with one it is nested in', () => {
  const css = styles.build(['[[open]>summary>&]:rotate-z-90']);
  assert.match(css, /\[open\]\s*>\s*summary\s*>\s*\.\\\[\\\[open\\\]\\>summary\\>\\&\\\]\\:rotate-z-90\s*\{/);
});

test('a wide screen\'s copy and its phone copy switch at one width, so exactly one of a pair shows', () => {
  const css = styles.build(['max-sm:hidden', 'sm:hidden']);
  const query = (cls) => css.match(new RegExp(`@media \\(([^)]*)\\)\\s*\\{\\s*\\.${cls}\\s*\\{\\s*display: none`))?.[1];
  const [, below] = query('max-sm\\\\:hidden')?.match(/^width < (\S+)$/) ?? [];
  assert.ok(below, 'max-sm:hidden hides below a width');
  assert.equal(query('sm\\\\:hidden'), `width >= ${below}`);
});

test('the chosen Usage period\'s fill replaces the ghost tint instead of losing to it', async () => {
  const { cn } = await import('../src/lib/cn.ts');
  const ghost = source('components/ui/button.tsx').match(/ghost:\s*'([^']*)'/)[1];
  const period = source('components/Usage.tsx').match(/PERIODS\.map\([^\n]*?<Button [^>]*?className="([^"]*)"/)[1];
  assert.deepEqual(cn(ghost, period).split(' ').filter((c) => c.startsWith('aria-pressed:bg-')), ['aria-pressed:bg-raised']);
});

// happy-dom loads no stylesheet, so the DOM tests (tests/dom) assert what a user or a screen reader gets: roles, names,
// text and state attributes. How a thing looks lives here, once: [file under src, the pattern its source keeps, what the
// pattern guarantees, false when the pattern must be absent]. A restyle that changes one of these updates this table.
const LOOKS = [
  ['components/Todos.tsx', /const RAIL: Record<TodoLink\['kind'\], string> = \{ loop: 'text-accent'/, 'a loop\'s rail on the todo board is in accent'],
  ['components/Plan.tsx', /\bborder-[bt]\b/, 'the plan reader has no hairlines', false],
  ['components/ui/panel.tsx', /\bborder-[bt]\b/, 'the anchored reader\'s parts have no hairlines', false],
  ['components/ui/reader.tsx', /\bborder-[bt]\b/, 'the anchored reader has no hairlines', false],
  ['components/Transcript.tsx', /pending \? '[^']*\bbg-raised\b[^']*' : 'bg-tint-well'/, 'a waiting question stays raised; only a settled one takes the darker tint-well'],
  ['components/Transcript.tsx', /answer: 'Not answered', tone: 'text-muted'/, 'a question a stopped turn left open reads "Not answered" in muted'],
  ['components/ui/collapse.tsx', /shown \|\| fading \? 'grid-rows-\[1fr\]' : 'grid-rows-\[0fr\]', fading && 'motion-reduce:grid-rows-\[0fr\]'/, 'a closing disclosure keeps its room, except under reduced motion'],
  ['components/ui/collapse.tsx', /fading && 'opacity-0 transition-opacity duration-100 /, 'a closing disclosure fades its content over fast'],
  ['components/ui/dialog.tsx', /keepMounted && !present && cn\([^)]*'\[content-visibility:hidden\]'\)/, 'a kept, closed sheet skips its content instead of hiding it'],
  ['components/Assist.tsx', /"pointer-events-none absolute inset-x-0 top-0 /, 'the suggestion sits over the composer, out of its flow, so the composer keeps its height'],
  ['components/Assist.tsx', /<span aria-hidden="true" className="line-clamp-2 /, 'the ghost text shows at most two lines'],
  ['components/Assist.tsx', /aria-label="Use suggestion" className="pointer-events-auto (?![^"]*\bhidden\b)/, 'the Use button takes the pointer and shows on every pointer'],
  ['components/StatusLine.tsx', /<span className="min-w-0 shrink-\[3\] truncate">Compacting in background<\/span>/, 'compaction in the background is words only, in the line\'s muted caption'],
  ['components/ComposerTools.tsx', /<Tabs\.Panel value=\{tab\} className=\{cn\('flex flex-none [^']*', sheet \? 'h-\[60dvh\]' : 'h-\[min\(440px,60vh\)\]'\)\}/, 'every Tools tab has one fixed height: the body never takes its content\'s height'],
  ['components/InlinePicker.tsx', /item\.disabled && 'opacity-45'/, 'a command that cannot run is dimmed like a disabled menu item'],
  ['components/Composer.tsx', /"relative flex h-7 [^"]*">\s*<div aria-hidden="true" [^>]*\/>\s*<Loading label="Loading the model catalog…" className="absolute /, 'the catalog\'s Loading line sits over a bar in the pickers\' 28px height, so the row does not move when they land'],
  ['components/Composer.tsx', /<span className="in-data-\[fold~=execution\]:hidden"> · \{execution\}/, 'the execution fold drops the mode\'s execution word'],
  ['components/Composer.tsx', /in-data-\[fold~=permissions\]:hidden">\s*\{permission\}/, 'the permissions fold drops the mode\'s label'],
  ['components/Composer.tsx', /id="composer-mode" [^>]*className=\{cn\('[^']*\bin-data-\[fold~=more\]:hidden\b/, 'the More fold moves the mode into More'],
  ['components/Composer.tsx', /<span className="in-data-\[fold~=tuning\]:hidden">\{tuningLabel\}/, 'the tuning fold drops the effort and context label'],
  ['components/Composer.tsx', /id="composer-effort-context" [^>]*className="[^"]*\bin-data-\[fold~=more\]:hidden\b/, 'the More fold moves effort and context into More'],
  ['components/Composer.tsx', /id="composer-more" [^>]*className="[^"]*\bsm:in-data-\[fold~=more\]:inline-flex\b/, 'the More fold shows More on a wide screen'],
  ['components/Composer.tsx', /data-squeeze="" className="[^"]*\bin-data-\[fold~=model\]:hidden\b/, 'the model fold drops the model\'s name last'],
  ['components/Composer.tsx', /data-fold="" className=\{cn\('[^']*\bdata-\[fold~=wrap\]:flex-wrap\b/, 'the wrap fold wraps the toolbar'],
  ['components/DataTable.tsx', /className=\{cn\('relative whitespace-nowrap', [^>]*>\s*\{row\.cells\[index\]\}\s*<svg aria-hidden="true" className=\{cn\('[^']*\btop-\[calc\(1lh\+2px\)\] h-0\.75 [^']*\bbg-accent-wash'[^>]*><rect [^>]*className="fill-accent\/50"/, 'a data bar runs on a slim track below its figure, never behind its digits, and the figure stays on one line'],
  ['components/common.tsx', /Finished: \{ icon: CircleCheck, tone: 'text-success', rung: 'done' \}/, 'Finished is green on its glyph'],
  ['components/common.tsx', /\(rung === 'done' \|\| rung === 'off'\) && \(fresh \? 'font-medium text-ink' : 'text-muted'\)/, 'a read Finished keeps its word muted; only its glyph is coloured'],
  ['components/common.tsx', /Working: \{ icon: WorkingMark, tone: 'text-accent', rung: 'live' \}/, 'Working is blue'],
  ['components/common.tsx', /\(rung === 'live' \|\| rung === 'bad'\) && tone,/, 'a live or bad state colours its word'],
  ['components/StatusLine.tsx', /\{elapsed && \(\s*<span className=\{cn\('shrink-0 tabular-nums max-sm:hidden'/, 'a phone hides the live line\'s elapsed'],
  ['components/StatusLine.tsx', /you \? 'text-attention' : 'text-body'/, 'the live line waiting on the owner is in the attention tone'],
  ['components/StatusLine.tsx', /you \? 'font-semibold' : waited \? 'text-muted' : 'text-ink'/, 'a model call past the wait threshold reads in muted words'],
  ['components/PlacePicker.tsx', /data-place="" className="(?![^"]*\bflex-1\b)[^"]*\bmin-w-0\b/, 'the place on the composer row shrinks and never grows to fill it'],
  ['components/Sidebar.tsx', /<TaskTitle session=\{session\} className=\{cn\('min-w-0 flex-1 truncate /, 'a Thread row\'s title stays on its one line'],
  ['components/Sidebar.tsx', /\{branch && <span className="[^"]*\btext-meta font-normal text-muted\b/, 'a Thread row\'s branch is quiet: muted, normal weight'],
  ['components/Sidebar.tsx', /selected \? 'bg-tint-selected' : 'group-hover\/row:bg-tint-hover'/, 'only the open Thread row is tinted; the others tint under the pointer'],
  ['components/Sidebar.tsx', /rowClass = cn\(\s*'flex min-h-11\.5 (?:(?!;)[\s\S])*?(shadow-|ring-|bg-raised|\blift\b)/, 'a Thread row is flat on the rail: no shadow, ring or raised surface', false],
  ['components/StatusLine.tsx', /<Blocks items=\{todoBlocks\(tally\)\} className="max-sm:hidden" \/>\s*<span className="shrink-0 tabular-nums">\s*<span className="max-sm:hidden">\{`\$\{tally\.done\} of \$\{tally\.total\}`\}<\/span>\s*<span className="sm:hidden">\{`\$\{tally\.done\}\/\$\{tally\.total\}`\}<\/span>/, 'the todo button: blocks and "1 of 4" on a wide screen, "1/4" on a phone'],
  ['components/StatusLine.tsx', /<span className="max-sm:hidden">\{plural\(turns\.length, 'turn'\)\}<\/span>\s*<span className="sm:hidden">\{turns\.length\}<\/span>/, 'the history button: "1 turn" on a wide screen, "1" on a phone'],
  ['components/StatusLine.tsx', /items=\{subagentBlocks\(subagents\)\} className="max-sm:hidden" \/>\s*<Blocks aria-hidden="true" items=\{subagentBlocks\(subagents\)\.filter\(\(b\) => b\.key === 'failed'\)\} className="sm:hidden" \/>\s*<span aria-hidden="true" className="[^"]*\bsm:hidden">/, 'the subagents button: every block on a wide screen; a phone keeps the failed one and "3/5"'],
  ['components/StatusLine.tsx', /list\?\.kind === 'carried' && 'opacity-55'/, 'the last turn\'s list, not touched yet, is dimmed on the strip'],
  ['components/StatusLine.tsx', /\{r\.removed && <span className="text-muted"> · removed from the list since<\/span>\}/, 'a row since removed says so in muted words'],
  ['components/Todos.tsx', /\{now && <span className="[^"]*\btext-accent">now<\/span>\}/, 'the row the work is at is marked "now" in accent'],
  ['components/Todos.tsx', /const CARD: Record<TodoStatus, string> = \{[^}]*\bblocked: 'border-warning'/, 'a blocked card on the board takes the warning border'],
  ['components/Changes.tsx', /<span className="min-w-0 grow basis-32 truncate text-ui" title=\{f\.path\}>\s*<span className="text-muted">\{dir\}<\/span><span className="font-medium text-ink">\{name\}<\/span>/, 'a card names its file in ink after its muted folder, on one line'],
  ['components/Changes.tsx', /<span className="line-clamp-2 (?![^"]*\btruncate\b)[^"]*">\s*<span className="whitespace-nowrap">\{files\.length\}[\s\S]*?<span className="whitespace-nowrap">\{viewedCount\} of \{files\.length\} viewed<\/span>/, 'the Changes head count wraps to two lines rather than clips, each fact whole'],
  ['components/Subagents.tsx', /<ul className="(?![^"]*\bcolumns-)[^"]*\bmax-w-2xl\b[^"]*">/, 'subagents at work are one column of rows, at most 2xl wide'],
  ['components/Subagents.tsx', /s\.status === 'failed' \? 'text-error' : needsYou\(text\) \? 'text-warning' : 'text-muted'/, 'a failed subagent\'s caption is its error, in the error tone'],
  ['components/Task.tsx', /<div className="group\/title flex min-w-28 flex-1 /, 'the Thread title keeps at least 7rem beside the header\'s buttons'],
  ['components/common.tsx', /<span className="text-success">\+<Num value=\{additions\} \/><\/span> <span className="text-error">−<Num value=\{deletions\} \/><\/span>/, 'line counts: added in the success tone, removed in the error tone'],
  ['components/DataTable.tsx', /<section aria-label=\{label \?\? `Table: [^`]*`\} [^>]*className="(?![^"]*overflow-x-hidden)[^"]*\boverflow-auto\b[^"]*">\s*<table className="data-table w-full">/, 'a table scrolls in its own box, down and sideways, at least as wide as the message'],
  ['components/DataTable.tsx', /<section aria-label="Table" [^>]*className="(?![^"]*overflow-x-hidden)[^"]*\boverflow-auto\b[^"]*"><table className="[^"]*\bw-full\b/, 'a streaming table scrolls in its own box, down and sideways, at least as wide as the message'],
  ['components/DataTable.tsx', /const alignment = \(align: CSSProperties\['textAlign'\]\) => align === 'right' \? 'text-right'/, 'a right-aligned column is right-aligned'],
  ['components/common.tsx', /export const BAR_BOX = '(?![^']*(shadow-|bg-raised|before:bg-))[^']*\bh-9\b/, 'the Thread header\'s bar is a flat 36px box'],
  ['components/Sidebar.tsx', /<header className=\{cn\(BAR_ROW[^>]*>\s*<div className="(?![^"]*(shadow-|bg-raised))[^"]*\bh-9\b/, 'the sidebar\'s top row is flat on the rail, in the same 36px box'],
  ['components/common.tsx', /rung === 'now' && !bare && \[chipVariants\(\{ tone: 'attention' \}\)/, 'the one filled tag is the attention chip itself, and the phone\'s glyph-only label has none'],
  ['components/ui/dialog.tsx', /export const readerPopupClass =\s*'(?![^']*border)/, 'an anchored reader has no border: its shadow lifts it'],
  ['components/TurnChanges.tsx', /label="This turn's changes" className="(?![^"]*border)/, 'the turn\'s changes reader has no border'],
  ['components/ui/button.tsx', /ghost:\s*'(?![^']*(?:^|\s)(?:bg-raised|shadow-))[^']*\bnot-aria-disabled:hover:bg-tint-hover\b/, 'a ghost button has no surface or shadow at rest and tints on hover'],
  ['components/ui/button.tsx', /sm: 'h-7 [^']*\bpointer-coarse:after:-inset-y-2\b/, 'a small button is 28px tall and 44px to a touch'],
  ['components/UsageButton.tsx', /<Button \{\.\.\.opener\} size="sm" className=\{cn\('(?![^']*(bg-raised|shadow-))[^']*\baria-expanded:bg-tint-selected\b/, 'the Usage and AI chip is flat, tinted while open'],
  ['components/UsageButton.tsx', /<Button \{\.\.\.opener\} className=\{cn\('[^']*\bflex-col\b/, 'the rail\'s Usage and AI button stacks the share over the calls'],
  ['components/UsageButton.tsx', /<Cpu aria-hidden="true" className=\{count \? 'text-accent' : 'text-faint'\} \/>/, 'the model calls glyph is accent while calls run, faint when idle'],
  ['components/UsageButton.tsx', /<Terminal aria-hidden="true" className="text-success" \/>/, 'Threads running locally are green'],
  ['components/Sidebar.tsx', /<div ref=\{scroller\} className="[^"]*\boverflow-y-auto\b/, 'the Thread list scrolls in its own box'],
  ['components/Sidebar.tsx', /<div className="max-h-\[40dvh\] overflow-y-auto /, 'an open shelf (Settled) scrolls on its own within 40% of the screen'],
  ['components/Sidebar.tsx', /if \(compact\) \{\s*rowClass = cn\((?:(?!\}\s*else)[\s\S])*!selected && cn\('text-muted /, 'a shelf row is quiet by colour'],
  ['components/Sidebar.tsx', /if \(compact\) \{\s*rowClass = cn\((?:(?!\}\s*else)[\s\S])*\bopacity-/, 'a shelf row is never quiet by opacity, so its title keeps AA', false],
  ['components/Attachments.tsx', /object-contain', fit \? 'w-\[min\(94vw,1400px,var\(--fit\)\)\]' : 'w-auto'\)/, 'the lightbox scales a sized drawing up to fit, keeping its shape, and shows a picture without a size at most at its own size'],
  ['components/Transcript.tsx', /<div className=\{cn\('absolute top-full [^']*\btext-stamp tabular-nums text-muted opacity-0 [^']*\bgroup-hover\/copy:opacity-100\b/, 'a message\'s time and copy sit under it out of the flow, in the muted stamp, shown on hover'],
  ['components/ui/blocks.tsx', /data-tone=\{tone\} className=\{cn\('[^']*\bbg-tint-hover\b/, 'a block\'s fill is neutral; its tone is on its glyph'],
  ['components/Transcript.tsx', /const HEAD = '(?![^']*(\babsolute\b|opacity-0|whitespace-nowrap|\bh-6\b))[^']*\bflex-wrap\b/, 'a turn\'s head is in the flow, always in view, and wraps'],
  ['components/Transcript.tsx', /<span data-outcome="" className="contents">/, 'a turn\'s outcome is part of its sign-off\'s own line'],
  ['components/Transcript.tsx', /^const SIGN = '(?![^']*(\babsolute\b|opacity-0))[^']*\bflex-wrap\b[^']*\bselect-none\b/m, 'a turn\'s sign-off is in the flow, always in view, wraps, and stays out of a copied selection'],
  ['index.css', /\.md > p:has\(\+ \.md-sign:not\(:empty\)\) \{\s*display: inline;/, 'a turn\'s sign-off follows the last paragraph\'s last words'],
  ['components/Receipts.tsx', /aria-label="Claims with no record in this turn" className="[^"]*\bbasis-full\b/, 'the claims with no record take a line of their own'],
  ['components/Transcript.tsx', /<span className="max-sm:hidden">\{result\.passed\} passed · <\/span>\{result\.failed\} failed/, 'a phone keeps a step\'s failures alone'],
  ['components/Transcript.tsx', /^const NOTES = "[^"]*\bbefore:fade-rule-y\b/m, 'a run of notes shares one fading rail'],
  ['components/Transcript.tsx', /data-note=\{note \? '' : undefined\} className=\{note \? 'text-ui text-muted [^']*' : 'text-chat text-body'\}/, 'a note is 13px and muted, never faint; a whole reply keeps the chat size'],
  ['components/Transcript.tsx', /failures\.map\(\(p\) => <span className="[^"]*\btext-error"/, 'a running turn\'s failures on its head are in the error tone'],
  ['components/common.tsx', /Error: \{ icon: TriangleAlert, tone: 'text-error', rung: 'bad' \}/, 'Error is red'],
];

test('the looks the DOM tests cannot see stay where they are set', () => {
  const broken = LOOKS.filter(([file, pattern, , present = true]) => pattern.test(source(file)) !== present).map(([file, , why]) => `${file}: ${why}`);
  assert.deepEqual(broken, []);
});
