package app

import (
	"context"
	"database/sql"
	"io/ioutil"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func doDirectoryTest(t *testing.T, f func(ctx context.Context, d *UserDirectory)) {
	tmpDir, err := ioutil.TempDir("", "changeset-chihuahua-test")
	require.NoError(t, err)
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Errorf("failed to clean up tmpdir: %v", err)
		}
	}()

	dbFile := path.Join(tmpDir, "directory.db")
	db, err := NewUserDirectory(zaptest.NewLogger(t), "sqlite:"+dbFile)
	require.NoError(t, err)
	require.NotNil(t, db)

	f(context.Background(), db)
}

func TestDirectoryBasics(t *testing.T) {
	const gerritUsername = "noodle"
	const chatID = "U1E9A928BCD"

	doDirectoryTest(t, func(ctx context.Context, db *UserDirectory) {
		// looking up a nonexistent name should fail
		got, err := db.LookupChatIDForGerritUser(ctx, gerritUsername)
		require.Equal(t, sql.ErrNoRows, err)
		require.Equal(t, "", got)

		// inserting a new name should work
		err = db.AssociateChatIDWithGerritUser(ctx, gerritUsername, chatID)
		require.NoError(t, err)

		// trying to insert again should fail
		err = db.AssociateChatIDWithGerritUser(ctx, gerritUsername, chatID)
		require.Error(t, err)

		// inserted name can be retrieved
		got, err = db.LookupChatIDForGerritUser(ctx, gerritUsername)
		require.NoError(t, err)
		require.Equal(t, chatID, got)
	})
}
