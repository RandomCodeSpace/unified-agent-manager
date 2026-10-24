package web

import (
	"encoding/json"
	"errors"
	"strings"
)

// parseAnswer accepts a prose preamble or a fenced object, but never a second
// answer. The JSON decoder handles balanced objects and escaped string braces.
func parseAnswer(reply string, v any) error {
	if len(reply) > 2048 {
		return errors.New("answer is too long")
	}
	reply = strings.TrimSpace(thinkRE.ReplaceAllString(reply, ""))
	lower := strings.ToLower(reply)
	if strings.Contains(lower, "i cannot") || strings.Contains(lower, "as an ai") {
		return errors.New("refusal is not an answer")
	}
	start := strings.IndexByte(reply, '{')
	if start < 0 {
		return errors.New("answer must contain one JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(reply[start:]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	tail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reply[start+int(dec.InputOffset()):]), "```"))
	if tail != "" {
		return errors.New("answer must contain only one object")
	}
	return nil
}

// recordAnswerLocked counts the job's answer toward the purpose's pause: a
// valid one, or an invalid one that ended the job. An invalid first answer
// is left to the retry, so a turn counts once.
func (l *utilityLog) recordAnswerLocked(call UtilityCall) {
	if call.Valid == nil || !*call.Valid && !call.Fallback || dayOf(call.At) != l.day {
		return
	}
	answers := append(l.answers[call.Purpose], *call.Valid)
	if len(answers) > 10 {
		answers = answers[len(answers)-10:]
	}
	l.answers[call.Purpose] = answers
	invalid := 0
	for _, valid := range answers {
		if !valid {
			invalid++
		}
	}
	if invalid >= 4 {
		l.paused[call.Purpose] = true
	}
}
