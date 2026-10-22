package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func TestCloseWaitsForCommitAndTriggersCoreTermination(t *testing.T) {
	s, dir, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	original := s.app
	reached, release := make(chan struct{}), make(chan struct{})
	s.app = stalledCompletion{original, reached, release}
	terminated := make(chan struct{}, 1)
	s.app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{Func: func(e *core.TerminateEvent) error {
		terminated <- struct{}{}
		return e.Next()
	}})
	defer s.app.OnTerminate().UnbindAll()
	committed := make(chan error, 1)
	input := CreditInput{"aaaaaaaaaaaaaaa", "5", key(now, 2)}
	go func() { _, err := s.Credit(context.Background(), input); committed <- err }()
	<-reached
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
		close(release)
		t.Fatal("storage closed before commit completed")
	case <-terminated:
		close(release)
		t.Fatal("termination started before commit completed")
	case <-time.After(20 * time.Millisecond):
	}
	if s.Ready() {
		close(release)
		t.Fatal("shutdown left readiness true")
	}
	_, err := s.Balance(background, "afterclose00000")
	codeIs(t, err, CodeClosed)
	close(release)
	if err := <-committed; err != nil {
		t.Fatal("shutdown erased observed commit")
	}
	if err := <-closed; err != nil {
		t.Fatal("joined storage closure failed")
	}
	select {
	case <-terminated:
	default:
		t.Fatal("supported termination hook not called")
	}
	reopened, err := Open(background, dir)
	must(t, reopened, err)
	defer reopened.Close()
	credit, err := reopened.Credit(background, input)
	if must(t, credit, err).BalanceAfter != "105" || balance(t, reopened, "aaaaaaaaaaaaaaa").AccountBalance.Balance != "105" {
		t.Fatal("joined restart duplicated or lost committed credit")
	}
}

func TestTerminationFailureIsClosedAndDoesNotReopenAccounting(t *testing.T) {
	s, _, _ := testService(t)
	s.app.OnTerminate().Bind(&hook.Handler[*core.TerminateEvent]{Id: "synthetic-close-failure", Func: func(e *core.TerminateEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return errors.New("SYNTHETIC_HOSTILE_CLOSE_EXCEPTION")
	}})
	defer s.app.OnTerminate().Unbind("synthetic-close-failure")
	err := s.Close()
	codeIs(t, err, CodeClose)
	if s.Ready() || Failure(err).Stage != "shutdown" || Failure(err).Outcome != "unavailable" {
		t.Fatal("termination failure changed lifecycle evidence")
	}
	_, err = s.Balance(background, "notopen00000000")
	codeIs(t, err, CodeClosed)
}
