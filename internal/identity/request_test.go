package identity

import (
	"net/http/httptest"
	"testing"
)

func deriveFor(t *testing.T, body map[string]any, scope string) RequestIDs {
	t.Helper()
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	return DeriveRequestIDs(request, body, scope)
}

func TestSameOpeningIsScopedPerCaller(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "你好"}},
	}
	first := deriveFor(t, body, "key-a")
	second := deriveFor(t, body, "key-b")
	if first.Session == second.Session {
		t.Fatalf("two callers with the same opening shared session %s", first.Session)
	}
	if first.Project == second.Project {
		t.Fatalf("two callers shared project %s", first.Project)
	}
	if first.Project == "" || first.Project == "mesh:default-project" {
		t.Fatal("the default project must carry the caller scope")
	}
}

func TestSameCallerKeepsSessionStable(t *testing.T) {
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "你好"}},
	}
	first := deriveFor(t, body, "key-a")
	second := deriveFor(t, body, "key-a")
	if first.Session != second.Session {
		t.Fatalf("the same caller and opening must keep one session: %s vs %s", first.Session, second.Session)
	}
}

func TestOfficialSessionIsKeptVerbatim(t *testing.T) {
	official := "ses_001122334455abcdefghijklmn"
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	request.Header.Set("x-opencode-session", official)
	ids := DeriveRequestIDs(request, map[string]any{}, "key-a")
	if ids.Session != official {
		t.Fatalf("official session was rewritten to %s", ids.Session)
	}
}

func TestSystemPromptSeparatesIdenticalOpenings(t *testing.T) {
	base := func(system string) map[string]any {
		return map[string]any{
			"system":   system,
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		}
	}
	first := deriveFor(t, base("you are a code reviewer"), "key-a")
	second := deriveFor(t, base("you are a translator"), "key-a")
	if first.Session == second.Session {
		t.Fatal("different system prompts must not share a session")
	}
}

func TestClaudeCodeSessionUUIDWins(t *testing.T) {
	const session = "123e4567-e89b-12d3-a456-426614174000"
	one := deriveFor(t, map[string]any{"metadata": map[string]any{
		"user_id": "user_hash_one_account_acct_session_" + session,
	}}, "key-a")
	two := deriveFor(t, map[string]any{"metadata": map[string]any{
		"user_id": "user_hash_two_account_acct_session_" + session,
	}}, "key-a")
	if one.Session != two.Session {
		t.Fatalf("the same Claude Code session must map to one id: %s vs %s", one.Session, two.Session)
	}
	other := deriveFor(t, map[string]any{"metadata": map[string]any{
		"user_id": "user_hash_one_account_acct_session_ffffffff-ffff-ffff-ffff-ffffffffffff",
	}}, "key-a")
	if other.Session == one.Session {
		t.Fatal("different Claude Code sessions must differ")
	}
}
