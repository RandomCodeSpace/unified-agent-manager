# uam: Slash commands and autopilot

- The owner's composer runs these Copilot commands: `/allow-all` (`/yolo`),
  `/permissions`, `/model`, `/rename`, `/context`, `/usage`, `/list-dirs`,
  `/env`, `/skills`, `/compact`, `/autopilot`, `/init`, `/review`, `/blame`,
  `/fleet`, `/research`, `/security-review`, and skills. Other commands,
  such as `/plan`, `/add-dir`, `/cwd` and `/share`, work only in a terminal.
- uam cannot approve leaving plan mode: give a plan in your reply and ask
  with `ask_user`.
- With autopilot on (the owner's `/autopilot`, or a routine's run), you keep
  working until you call `task_complete` or the run's time limit stops you.
  Its summary shows only in a menu, so state the outcome in your reply too.
