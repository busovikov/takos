package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestWS(t *testing.T) *Workspace {
	t.Helper()
	ws, err := Init(filepath.Join(t.TempDir(), "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestInitScaffold(t *testing.T) {
	ws := newTestWS(t)
	active, err := ws.ActiveSprint()
	if err != nil {
		t.Fatal(err)
	}
	next, err := ws.NextSprint()
	if err != nil {
		t.Fatal(err)
	}
	if active.Name != "sprint-1" || next.Name != "sprint-2" {
		t.Errorf("спринты: %s / %s", active.Name, next.Name)
	}
	if active.Meta.EndsAt.Sub(active.Meta.StartedAt) != 14*24*time.Hour {
		t.Errorf("длина спринта: %v", active.Meta.EndsAt.Sub(active.Meta.StartedAt))
	}
	if _, err := os.Stat(filepath.Join(active.Dir, "3-implementation")); err != nil {
		t.Error("нет стадийной папки 3-implementation")
	}
	// Идемпотентность.
	if _, err := Init(ws.Root); err != nil {
		t.Fatalf("повторный Init: %v", err)
	}
}

func TestCreateTaskAndSymlink(t *testing.T) {
	ws := newTestWS(t)
	task, pos, err := ws.CreateTask("Добавить экспорт отчётов в CSV", CreateOpts{Repo: "/tmp/r"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Meta.ID != "T-0001" {
		t.Errorf("id = %s", task.Meta.ID)
	}
	if pos.Stage != "refinement" || pos.Area != AreaSprint {
		t.Errorf("позиция: %+v", pos)
	}
	// Хендлер — симлинк, разрешающийся в папку задачи.
	fi, err := os.Lstat(pos.Path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("хендлер не симлинк: %v %v", fi, err)
	}
	resolved, err := filepath.EvalSymlinks(pos.Path)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.EvalSymlinks(task.Dir)
	if resolved != wantDir {
		t.Errorf("симлинк ведёт в %s, ожидалось %s", resolved, wantDir)
	}
	// Через симлинк читается task.md.
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("task.md через симлинк недоступен: %v", err)
	}

	// Вторая задача — в беклог.
	_, pos2, err := ws.CreateTask("Тёмная тема", CreateOpts{Backlog: true})
	if err != nil {
		t.Fatal(err)
	}
	if pos2.Stage != Backlog {
		t.Errorf("позиция беклог-задачи: %+v", pos2)
	}
}

func TestMoveHappyPathAndGates(t *testing.T) {
	ws := newTestWS(t)
	task, _, err := ws.CreateTask("Fix flaky auth test", CreateOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id := task.Meta.ID

	// refinement → planning: scaffold plan.md.
	if _, err := ws.MoveTask(id, "planning", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(task.Dir, PlanFile)); err != nil {
		t.Error("plan.md не заскаффолжен при входе в planning")
	}

	// planning → implementation закрыт гейтом plan-resolved.
	if _, err := ws.MoveTask(id, "implementation", "user", ""); err == nil {
		t.Fatal("переход должен быть закрыт гейтом plan-resolved")
	}
	pos, _ := ws.FindHandler(id)
	if pos.Stage != "planning" {
		t.Errorf("после отказа хендлер сместился: %s", pos.Stage)
	}
	events, _ := ReadHistory(task.Dir)
	for _, e := range events {
		if e.Verb == "move" && strings.Contains(e.Args, "implementation") {
			t.Error("отказ записал move в history")
		}
	}

	// resolve → переход открывается.
	if err := SetCheck(task.Dir, "plan-resolved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "implementation", "user", ""); err != nil {
		t.Fatal(err)
	}

	// Запрещённый переход (не в whitelist).
	if _, err := ws.MoveTask(id, "done", "user", ""); err == nil {
		t.Fatal("implementation → done не должен быть разрешён")
	}

	// implementation → review; review → done руками закрыт (manual: false).
	if _, err := ws.MoveTask(id, "review", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "done", "user", ""); err == nil {
		t.Fatal("review → done должен быть только автоматическим")
	}
	// …а автоматике можно.
	if _, err := ws.MoveTask(id, "done", "engine", ""); err != nil {
		t.Fatal(err)
	}
	stage, _ := LastMove(mustHistory(t, task.Dir))
	if stage != "done" {
		t.Errorf("итоговая стадия по history: %s", stage)
	}
}

func TestOpenReviewBlocksMoves(t *testing.T) {
	ws := newTestWS(t)
	task, _, _ := ws.CreateTask("Задача с ревью", CreateOpts{})
	id := task.Meta.ID
	ws.MoveTask(id, "planning", "user", "")
	SetCheck(task.Dir, "plan-resolved")
	ws.MoveTask(id, "implementation", "user", "")
	ws.MoveTask(id, "review", "user", "")

	if _, err := AddComment(task.Dir, id, "Кнопка видна для пустых отчётов"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.MoveTask(id, "done", "engine", ""); err == nil {
		t.Fatal("open-замечание должно блокировать перемещение")
	}

	// Триаж: resolve единственного замечания → путь открыт.
	round, path, err := LoadLatestRound(task.Dir)
	if err != nil || round == nil {
		t.Fatalf("раунд не найден: %v", err)
	}
	round.SetStatus("R1", ReviewResolved)
	SaveRound(path, round)
	if v, done := round.Verdict(); !done || v != "done" {
		t.Errorf("вердикт: %s %v", v, done)
	}
	if _, err := ws.MoveTask(id, "done", "engine", ""); err != nil {
		t.Fatal(err)
	}
}

func TestBacklogTakeAndSend(t *testing.T) {
	ws := newTestWS(t)
	task, _, _ := ws.CreateTask("Из беклога", CreateOpts{Backlog: true})
	id := task.Meta.ID

	// backlog → refinement в СЛЕДУЮЩИЙ спринт (take).
	if _, err := ws.MoveTask(id, "refinement", "user", "next"); err != nil {
		t.Fatal(err)
	}
	pos, _ := ws.FindHandler(id)
	if pos.Sprint != "sprint-2" || pos.Stage != "refinement" {
		t.Errorf("после take: %+v", pos)
	}
	// Симлинк валиден на новой глубине.
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("симлинк битый после take: %v", err)
	}

	// Обратно в беклог (b).
	if _, err := ws.MoveTask(id, Backlog, "user", ""); err != nil {
		t.Fatal(err)
	}
	pos, _ = ws.FindHandler(id)
	if pos.Area != AreaBacklog {
		t.Errorf("после b: %+v", pos)
	}
}

func TestCloseActiveSprint(t *testing.T) {
	ws := newTestWS(t)
	doneTask, _, _ := ws.CreateTask("Готовая", CreateOpts{})
	wip, _, _ := ws.CreateTask("Недоделанная", CreateOpts{})

	// Готовую доводим до done автоматикой.
	ws.MoveTask(doneTask.Meta.ID, "planning", "user", "")
	SetCheck(doneTask.Dir, "plan-resolved")
	ws.MoveTask(doneTask.Meta.ID, "implementation", "user", "")
	ws.MoveTask(doneTask.Meta.ID, "review", "user", "")
	ws.MoveTask(doneTask.Meta.ID, "done", "engine", "")
	// Недоделанную — в planning.
	ws.MoveTask(wip.Meta.ID, "planning", "user", "")

	closed, promoted, carried, err := ws.CloseActiveSprint(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if closed != "sprint-1" || promoted != "sprint-2" || carried != 1 {
		t.Errorf("close: %s %s carried=%d", closed, promoted, carried)
	}

	// Done-задача в архиве, стадия сохранена, симлинк жив.
	pos, err := ws.FindHandler(doneTask.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pos.Area != AreaArchive || pos.Stage != "done" {
		t.Errorf("done-задача: %+v", pos)
	}
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("симлинк в архиве битый: %v", err)
	}

	// Недоделанная — в новом активном, та же стадия.
	pos, _ = ws.FindHandler(wip.Meta.ID)
	if pos.Sprint != "sprint-2" || pos.Stage != "planning" {
		t.Errorf("перенесённая: %+v", pos)
	}
	if _, err := os.Stat(filepath.Join(pos.Path, TaskFile)); err != nil {
		t.Errorf("симлинк перенесённой битый: %v", err)
	}

	// Новый следующий создан, активный один.
	active, _ := ws.ActiveSprint()
	next, _ := ws.NextSprint()
	if active.Name != "sprint-2" || next.Name != "sprint-3" {
		t.Errorf("после close: active=%s next=%s", active.Name, next.Name)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix flaky auth test": "fix-flaky-auth-test",
		"Добавить экспорт отчётов в CSV": "dobavit-eksport-otchetov-v-csv",
		"  ---  ": "task",
		"Очень длинное название задачи которое не влезает в лимит символов слага": "ochen-dlinnoe-nazvanie-zadachi-kotoroe-n",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMdSections(t *testing.T) {
	body := "## Objective\nСтарый текст.\n"
	body = SetSection(body, "Context", "Найдено в src/x.go")
	body = SetSection(body, "Objective", "Новый текст.")
	if got := GetSection(body, "Objective"); got != "Новый текст." {
		t.Errorf("Objective = %q", got)
	}
	if got := GetSection(body, "Context"); got != "Найдено в src/x.go" {
		t.Errorf("Context = %q", got)
	}
	if got := GetSection(body, "Missing"); got != "" {
		t.Errorf("Missing = %q", got)
	}
}

func mustHistory(t *testing.T, dir string) []HistoryEvent {
	t.Helper()
	events, err := ReadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	return events
}
