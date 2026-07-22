package im

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type imSessionLoadStub struct {
	interfaces.SessionService
	session       *types.Session
	consoleCalled bool
	tenantID      uint64
	sessionID     string
}

func (s *imSessionLoadStub) GetSession(context.Context, string) (*types.Session, error) {
	s.consoleCalled = true
	return nil, errors.New("console read gate rejected IM session")
}

func (s *imSessionLoadStub) GetSessionByID(_ context.Context, tenantID uint64, sessionID string) (*types.Session, error) {
	s.tenantID = tenantID
	s.sessionID = sessionID
	return s.session, nil
}

func TestGetSessionForIMUsesTenantScopedInternalLookup(t *testing.T) {
	want := &types.Session{ID: "session-1", TenantID: 42}
	stub := &imSessionLoadStub{session: want}
	svc := &Service{sessionService: stub}

	got, err := svc.getSessionForIM(context.Background(), 42, "session-1")
	if err != nil {
		t.Fatalf("getSessionForIM() error = %v", err)
	}
	if got != want {
		t.Fatalf("getSessionForIM() = %#v, want %#v", got, want)
	}
	if stub.consoleCalled {
		t.Fatal("getSessionForIM called console-scoped GetSession")
	}
	if stub.tenantID != 42 || stub.sessionID != "session-1" {
		t.Fatalf("GetSessionByID args = (%d, %q), want (42, session-1)", stub.tenantID, stub.sessionID)
	}
}
