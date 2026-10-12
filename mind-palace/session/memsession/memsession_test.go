package memsession_test

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessiontest"
)

func TestConformance(t *testing.T) {
	sessiontest.Run(t, func(*testing.T) session.Store { return memsession.New() })
}
