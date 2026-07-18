package core

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// history.log — летопись задачи: строка на событие, фиксированная грамматика
//
//	<RFC3339> [<actor>] <verb> <args>
//
// Читается человеком, дифается git'ом, парсится одним регэкспом. Позиция
// хендлера остаётся единственным авторитетом состояния; журнал — история.

const HistoryFile = "history.log"

// HistoryEvent is one parsed line of history.log.
type HistoryEvent struct {
	At    time.Time
	Actor string // user | engine | rule:<id> | agent:<role>
	Verb  string // created | move | spawn | session | check | reject | …
	Args  string
}

func (e HistoryEvent) String() string {
	return fmt.Sprintf("%s [%s] %s %s",
		e.At.UTC().Format(time.RFC3339), e.Actor, e.Verb, e.Args)
}

var historyRe = regexp.MustCompile(`^(\S+) \[([^\]]+)\] (\S+) ?(.*)$`)

// AppendHistory appends one event to taskDir/history.log. A single write of a
// full line keeps the log crash-safe: a torn tail is dropped by the parser.
func AppendHistory(taskDir, actor, verb, args string) error {
	e := HistoryEvent{At: time.Now(), Actor: actor, Verb: verb, Args: strings.TrimSpace(args)}
	f, err := os.OpenFile(filepath.Join(taskDir, HistoryFile),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(e.String() + "\n")
	return err
}

// ParseHistory reads events from r. Lines that don't match the grammar
// (including a torn last line without newline) are skipped silently.
func ParseHistory(r io.Reader) ([]HistoryEvent, error) {
	var events []HistoryEvent
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := historyRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		at, err := time.Parse(time.RFC3339, m[1])
		if err != nil {
			continue
		}
		events = append(events, HistoryEvent{At: at, Actor: m[2], Verb: m[3], Args: m[4]})
	}
	return events, sc.Err()
}

// ReadHistory parses taskDir/history.log; a missing file is an empty history.
func ReadHistory(taskDir string) ([]HistoryEvent, error) {
	f, err := os.Open(filepath.Join(taskDir, HistoryFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseHistory(f)
}

// LastMove returns the destination stage of the latest successful move (or
// created) event — the reconciler compares it with the handler's position.
func LastMove(events []HistoryEvent) (stage string, ok bool) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		switch e.Verb {
		case "move":
			// "move <from> -> <to>"
			if parts := strings.Split(e.Args, "->"); len(parts) == 2 {
				return strings.TrimSpace(parts[1]), true
			}
		case "created":
			// "created <stage>" | "created backlog"
			if f := strings.Fields(e.Args); len(f) > 0 {
				return f[0], true
			}
		}
	}
	return "", false
}
