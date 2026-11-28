package control

import (
	"reflect"
	"testing"
	"time"
)

func TestRenewCompletionPreservesHistoryAndRejectsConflicts(t *testing.T) {
	at := time.Now().UTC()
	base := File{Enabled: true, Completed: true, Note: "historical question", OpenQuestion: &OpenQuestion{Note: "historical question", At: at}, UpdatedAt: at}
	for _, c := range []*Completion{nil, {DeclaredAt: at}, {DeclaredAt: at, GoalID: "goal_0123456789abcdef0123456789abcdef", GoalRevision: 2}} {
		f := base
		f.Completion = c
		next, err := RenewCompletion(f)
		if err != nil {
			t.Fatal(err)
		}
		next.Completed = true
		if !reflect.DeepEqual(f, next) {
			t.Fatal("renewal changed historical fields")
		}
	}
	conflicts := map[string]func(*File){
		"disabled": func(f *File) { f.Enabled = false }, "not_completed": func(f *File) { f.Completed = false },
		"pause": func(f *File) { f.Paused = true }, "stop": func(f *File) { f.Stop = true }, "await": func(f *File) { f.AwaitingUser = true },
		"episode": func(f *File) { f.EpisodeHold = true }, "brief": func(f *File) { f.BriefHold = []Brief{{Action: "handoff"}} },
		"question": func(f *File) { f.Question = &Question{} }, "stop_lanes": func(f *File) { f.StopLanesAt = &at }, "drop_queued": func(f *File) { f.StopLanesDropQueued = true },
		"transfer": func(f *File) { f.PendingTransfer = &PendingBBTransfer{} }, "superseded": func(f *File) { f.SupersededBy = "thr_example" },
		"partial_goal":       func(f *File) { f.Completion = &Completion{DeclaredAt: at, GoalRevision: 1} },
		"partial_assignment": func(f *File) { f.Completion = &Completion{DeclaredAt: at, AssignmentRevision: 1} },
		"zero_time":          func(f *File) { f.Completion = &Completion{} }, "bad_goal": func(f *File) { f.Completion = &Completion{DeclaredAt: at, GoalID: "invalid", GoalRevision: 1} },
		"bad_assignment": func(f *File) {
			f.Completion = &Completion{DeclaredAt: at, Assignments: "invalid", AssignmentRevision: 1}
		},
		"owner": func(f *File) { f.Ownership = &Ownership{ThreadID: "invalid", Generation: 1} }, "question_time": func(f *File) { f.OpenQuestion = &OpenQuestion{} },
	}
	for name, change := range conflicts {
		t.Run(name, func(t *testing.T) {
			f := base
			change(&f)
			next, err := RenewCompletion(f)
			if err == nil || !reflect.DeepEqual(f, next) {
				t.Fatal("conflict changed control", next, err)
			}
		})
	}
}
