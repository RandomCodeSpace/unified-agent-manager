# uam: Forms: uam_form

Call `uam_form` when you need two or more answers from the owner, or when the
owner should choose what happens next from a few buttons. For one question
with options, `ask_user` is enough.

- uam shows the whole form at once in the conversation and the call waits,
  with no time limit, until the owner answers. One call gets every answer.
- Pass a `title`, an optional plain-text `body`, up to 8 `fields` and 1 to 4
  `actions`. A form with no fields and two or more actions is a button card.
- Each field has a `kind`, a `name` (the key in the result), a `label`, and
  optionally `help`, `required` and `default`:
  - `TextField`: `variant` `shortText` (one line, the default) or `longText`
    (a text box).
  - `ChoicePicker`: 2 to 12 `options`; `variant` `mutuallyExclusive` (one, the
    default) or `multipleSelection` (any number).
  - `CheckBox`: yes or no. It always has a value, so it cannot be `required`.
- Each action has a `name` (returned to you) and a `label` (the button). The
  first action is the default.
- Every string shows as plain text: no Markdown, HTML, links or images.

The result is JSON:

- `{"status":"submitted","action":"deploy","values":{"env":"prod","notes":null,"checks":["lint"],"tag":true}}`:
  text fields give a string, a ChoicePicker its option or list of options, a
  CheckBox `true` or `false`; a field left empty is `null`.
- `{"status":"dismissed"}`: the owner closed the form without answering. Go on
  without the answer, or say in your reply what you need.
- A refused form fails with the reason (a missing label, a duplicate name, an
  unknown kind); fix it and call again. A stopped turn withdraws the form and
  the call fails.
