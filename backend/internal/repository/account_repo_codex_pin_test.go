package repository

import (
	"context"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLockedAccountUpdateKeepsLatestCodexPins(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		withCurrent, sameIdentity, credentialsUnchanged bool
	}{
		{"concurrent renewal wins", true, true, true},
		{"removed pin stays removed", false, true, true},
		{"different ChatGPT identity drops pin", true, false, false},
		{"token or proxy edit keeps identity pin", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			defer client.Close()
			var current any
			if tc.withCurrent {
				current = []byte(`{"gpt-a":{"state":"new-pin"},"gpt-b":{"state":"other-pin"}}`)
			}
			mock.ExpectQuery(`(?s)SELECT.*extra -> 'pinned_codex_turn_states'.*FOR NO KEY UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"identity", "ollama_identity", "proxy", "enabled", "rate", "snapshot", "session", "auto", "ollama_snapshot", "codex_pins", "codex_identity"}).AddRow(tc.credentialsUnchanged, false, true, nil, nil, nil, nil, nil, nil, current, tc.sameIdentity))
			account := &service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{service.PinnedCodexTurnStatesExtraKey: map[string]any{"gpt-a": "stale-pin"}, "custom": "updated"}}
			got, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "updated", got["custom"])
			if tc.withCurrent && tc.sameIdentity {
				require.Equal(t, map[string]any{"gpt-a": map[string]any{"state": "new-pin"}, "gpt-b": map[string]any{"state": "other-pin"}}, got[service.PinnedCodexTurnStatesExtraKey])
			} else {
				require.NotContains(t, got, service.PinnedCodexTurnStatesExtraKey)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
