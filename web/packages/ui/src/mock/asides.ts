// The asides kept with the mock Tasks: t3 "Doctor: add terminal line" has three, so its turns show chips.
import type { Aside } from '../api';

const NOW = Date.now();
const ago = (min: number) => new Date(NOW - min * 60000).toISOString();

export function seedAsides(): Record<string, Aside[]> {
  return {
    t3: [
      {
        id: 'aside-1',
        // Asked while the agent was still in its first turn, right after the edit.
        turn: 'i1',
        working: true,
        asked_at: ago(53),
        answered_at: ago(52.8),
        question: 'Will the new row also show in `uam doctor --json`?',
        answer:
          'Not yet. The JSON output is built in `doctorJSON()` in `cmd/doctor.go`, and this turn only added the `printRow` call, which feeds the text table. It needs a `"terminal"` key there as well; ask the agent if you want both outputs to match.',
      },
      {
        id: 'aside-2',
        // Asked after the first turn ended, before the next prompt.
        turn: 'i1',
        asked_at: ago(41.5),
        answered_at: ago(41.3),
        question: 'Where does `term.Describe()` get the glyph set from?',
        answer:
          'From the CPR probe in `internal/term/probe.go`. At startup it prints a wide glyph, asks the terminal for the cursor position and checks whether the cursor moved one column or two. The result is cached for the process, so `Describe()` only formats it, which is why this machine reads `Windows Terminal · wide glyphs`.\n\nA terminal that never answers the probe falls back to ASCII after 300 ms.',
      },
      {
        id: 'aside-3',
        turn: 'i5',
        asked_at: ago(38),
        answered_at: ago(37.8),
        question: 'Would anything change for an SSH session without a TTY?',
        answer:
          'A little. Without a TTY the probe has nothing to write to, so `Describe()` reports `no tty · ASCII glyphs` and the doctor row says that. The new test only fakes `TERM=dumb`; the no-TTY case is not covered yet.',
      },
    ],
  };
}
