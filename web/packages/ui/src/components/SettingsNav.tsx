import { ChevronRight, Search } from 'lucide-react';
import { Fragment, useId, type KeyboardEvent } from 'react';
import { cn } from '../lib/cn';
import { Dot, Note } from './common';
import { Input } from './ui/input';

/** Where a group's settings are kept, said under each of its pages. */
export type SettingsScope = 'service' | 'browser' | 'copilot';

/**
 * The Settings pages, in groups (DESIGN.md Settings view). `keywords` are words a page holds that its label does not
 * say, for Find a setting.
 */
export const SETTINGS_GROUPS = [
  { label: 'Tasks', scope: 'service', pages: [
    { id: 'new-tasks', label: 'New tasks', blurb: 'The model, effort, mode and compaction a new task starts with.', keywords: 'model effort reasoning context size long mode safe autopilot compact compaction threshold default start' },
    { id: 'composer', label: 'Composer', blurb: 'What Enter does while a task runs, and suggested replies.', keywords: 'enter send now after this turn queue steer suggest suggested replies keyboard' },
    { id: 'archived', label: 'Archived', blurb: 'Archived tasks, read-only for good.', keywords: 'archive archived read-only delete export run again old tasks' },
  ] },
  { label: 'Models & AI', scope: 'service', pages: [
    { id: 'models', label: 'Models', blurb: 'The models the menus offer, and the ones subagents may use.', keywords: 'hide show hidden visible menu subagent subagents fallback allow list' },
    { id: 'background-ai', label: 'Background AI', blurb: "UAM's own AI calls: titles, suggested replies, outcome lines and drafts.", keywords: 'utility model cheapest none titles title daily limit calls budget paused credits log' },
    { id: 'token-costs', label: 'Token costs', blurb: 'The prices used to estimate what tasks cost.', keywords: 'price prices usd dollars tokens cost costs cache input output estimate' },
  ] },
  { label: 'Accounts', scope: 'service', pages: [
    { id: 'copilot', label: 'GitHub Copilot', blurb: 'Sign-in and the Copilot CLI on the server.', keywords: 'sign in sign out login token account link unlink cli update version' },
    { id: 'gitlab', label: 'GitLab', blurb: 'Check glab sign-in, instance and access on the server.', keywords: 'glab gitlab account host instance token scopes permissions expiry expires project role access authentication' },
    { id: 'providers', label: 'Custom providers', blurb: 'Your own OpenAI-compatible endpoints and keys.', keywords: 'byom api key keys endpoint openai compatible base url provider ollama environment variable custom models' },
  ] },
  { label: 'Agent toolkit', scope: 'copilot', pages: [
    { id: 'agents', label: 'Agents', blurb: 'Named agents a task can switch to.', keywords: 'custom agent persona' },
    { id: 'skills', label: 'Skills', blurb: 'Know-how and scripts agents load when they need them.', keywords: 'skill npx install SKILL.md' },
    { id: 'hooks', label: 'Hooks', blurb: 'Commands that run on agent events.', keywords: 'hook event pre tool post command' },
    { id: 'instructions', label: 'Instructions', blurb: 'AGENTS.md and copilot-instructions.md.', keywords: 'agents.md copilot-instructions system prompt rules' },
    { id: 'mcp', label: 'MCP servers', blurb: 'Tools agents call from other programs and services.', keywords: 'mcp server servers tools http sse command github' },
  ] },
  { label: 'Server', scope: 'service', pages: [
    { id: 'uam', label: 'UAM and access', blurb: 'Project folders, the version and restart, terminal access, and signing out.', keywords: 'default project location folder directory home path version restart update binary terminal shell access log out logout sign out session' },
    { id: 'connections', label: 'Connected instances', blurb: 'Other UAM servers to switch to.', keywords: 'instance instances federation pair remote server connect' },
  ] },
  { label: 'This browser', scope: 'browser', pages: [
    { id: 'browser', label: 'Appearance & alerts', blurb: 'Notifications, theme, motion and activity density.', keywords: 'theme dark light system motion animation reduced activity compact detailed density notifications notify push alerts' },
  ] },
] as const satisfies readonly { label: string; scope: SettingsScope; pages: readonly { id: string; label: string; blurb: string; keywords: string }[] }[];

export type SettingsPage = (typeof SETTINGS_GROUPS)[number]['pages'][number]['id'];
type Group = (typeof SETTINGS_GROUPS)[number];
type Page = Group['pages'][number];

export const SETTINGS_PAGES: readonly (Page & { group: Group })[] = SETTINGS_GROUPS.flatMap((group) => group.pages.map((page) => ({ ...page, group })));

export function isSettingsPage(id: unknown): id is SettingsPage {
  return SETTINGS_PAGES.some((page) => page.id === id);
}

/** Every word of the query is in the page's group, label, blurb, keywords or current values. */
export function pageMatches(page: Page & { group: Group }, query: string, summary = ''): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const text = [page.group.label, page.label, page.blurb, page.keywords, summary].join(' ').toLowerCase();
  return words.every((word) => text.includes(word));
}

/** Something on a page needs the user (the rail's dot); `text` is what it says to a screen reader. */
export interface PageAttention {
  tone: 'warning' | 'error';
  text: string;
}

/**
 * The Settings rail: Find a setting, then the pages by group. On a phone (`list`) it is the whole view, each page with
 * its current values under its name and a chevron to open it.
 */
export function SettingsRail({ pages, current, query, onQuery, onOpen, attention, summary, list }: Readonly<{
  pages: readonly (Page & { group: Group })[];
  /** The page on screen; none while searching, or on a phone's list. */
  current: SettingsPage | null;
  query: string;
  onQuery: (query: string) => void;
  onOpen: (page: SettingsPage) => void;
  attention: Partial<Record<SettingsPage, PageAttention>>;
  summary: Partial<Record<SettingsPage, string>>;
  list: boolean;
}>) {
  const id = useId();
  const shown = pages.filter((page) => pageMatches(page, query, summary[page.id]));
  const groups = SETTINGS_GROUPS.map((group) => ({ group, pages: shown.filter((page) => page.group === group) })).filter((g) => g.pages.length);
  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    // Escape clears the search first; an empty field lets it through (it closes Settings).
    if (event.key === 'Escape' && query) {
      event.preventDefault();
      event.stopPropagation();
      onQuery('');
    }
  }
  return (
    <nav aria-label="Settings sections" className={cn('flex min-h-0 shrink-0 flex-col gap-5 overflow-y-auto overflow-x-hidden overscroll-contain px-3 pt-2 pb-6', list ? 'w-full' : 'w-64')}>
      <div className="relative">
        <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-faint" />
        <Input type="search" size="md" aria-label="Find a setting" placeholder="Find a setting" spellCheck={false} autoComplete="off" className="w-full pl-8" value={query} onChange={(e) => onQuery(e.target.value)} onKeyDown={onKeyDown} />
      </div>
      {groups.length === 0 && <Note role="status" className="px-2">No setting matches “{query.trim()}”.</Note>}
      {groups.map(({ group, pages: items }) => (
        <div key={group.label} role="group" aria-labelledby={`${id}-${group.label}`} className="flex flex-col gap-0.5">
          <div id={`${id}-${group.label}`} className="px-2 pb-1 text-eyebrow uppercase text-muted">{group.label}</div>
          {items.map((page) => {
            const mark = attention[page.id];
            const on = page.id === current;
            // The current values describe the row on a phone; the button's name stays the page's.
            const describedBy = [list && `${id}-${page.id}-values`, mark && `${id}-${page.id}-mark`].filter(Boolean).join(' ') || undefined;
            return (
              <Fragment key={page.id}>
                <button type="button" data-settings-page={page.id} aria-current={on ? 'page' : undefined} aria-describedby={describedBy} onClick={() => onOpen(page.id)} className={cn('flex min-h-8 items-center gap-2 rounded-md px-2 text-left text-ui transition-colors pointer-coarse:min-h-11', on ? 'bg-tint-selected font-medium text-ink' : 'text-body hover:bg-tint-hover')}>
                  <span className="flex min-w-0 flex-1 flex-col py-1">
                    <span className="truncate">{page.label}</span>
                    {list && <span id={`${id}-${page.id}-values`} aria-hidden="true" className="truncate text-caption text-muted">{summary[page.id] ?? page.blurb}</span>}
                  </span>
                  {mark && <Dot tone={mark.tone} />}
                  {list && <ChevronRight aria-hidden="true" className="size-4 shrink-0 text-faint" />}
                </button>
                {mark && <span id={`${id}-${page.id}-mark`} className="sr-only">{mark.text}</span>}
              </Fragment>
            );
          })}
        </div>
      ))}
    </nav>
  );
}
