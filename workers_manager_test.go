package tcp

import (
	"testing"

	"github.com/roadrunner-server/errors"
	"github.com/stretchr/testify/require"
)

func TestPluginAddWorker(t *testing.T) {
	t.Run("delegates to the pool", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{}}
		require.NoError(t, p.AddWorker())
	})

	t.Run("passes the pool error through", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{addErr: errors.Str("allocation refused")}}
		require.EqualError(t, p.AddWorker(), "allocation refused")
	})
}

func TestPluginRemoveWorker(t *testing.T) {
	t.Run("delegates to the pool", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{}}
		require.NoError(t, p.RemoveWorker(t.Context()))
	})

	t.Run("passes the pool error through", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{removeErr: errors.Str("last worker kept")}}
		require.EqualError(t, p.RemoveWorker(t.Context()), "last worker kept")
	})
}
