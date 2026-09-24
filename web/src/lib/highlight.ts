import bash from 'highlight.js/lib/languages/bash';
import css from 'highlight.js/lib/languages/css';
import diff from 'highlight.js/lib/languages/diff';
import dockerfile from 'highlight.js/lib/languages/dockerfile';
import go from 'highlight.js/lib/languages/go';
import ini from 'highlight.js/lib/languages/ini';
import javascript from 'highlight.js/lib/languages/javascript';
import json from 'highlight.js/lib/languages/json';
import makefile from 'highlight.js/lib/languages/makefile';
import markdown from 'highlight.js/lib/languages/markdown';
import plaintext from 'highlight.js/lib/languages/plaintext';
import python from 'highlight.js/lib/languages/python';
import rust from 'highlight.js/lib/languages/rust';
import shell from 'highlight.js/lib/languages/shell';
import sql from 'highlight.js/lib/languages/sql';
import typescript from 'highlight.js/lib/languages/typescript';
import xml from 'highlight.js/lib/languages/xml';
import yaml from 'highlight.js/lib/languages/yaml';
import { createLowlight } from 'lowlight';

/**
 * Code highlighting, loaded on the first code block that names a language. lowlight runs
 * highlight.js and returns a tree rather than HTML, so the page never sets innerHTML; the
 * `hljs-*` classes are coloured with the tokens in `index.css`. Grammars carry their own
 * aliases (`ts`, `sh`, `yml`, `html`, `golang`, ...).
 */
const lowlight = createLowlight({ bash, css, diff, dockerfile, go, ini, javascript, json, makefile, markdown, plaintext, python, rust, shell, sql, typescript, xml, yaml });

export type HighlightTree = ReturnType<typeof lowlight.highlight>;

/** The highlighted tree, or null when the language is unknown here. */
export function highlight(language: string, code: string): HighlightTree | null {
  return lowlight.registered(language) ? lowlight.highlight(language, code) : null;
}
