package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"fincore/internal/testutil"
)

func TestModeValidation(t *testing.T) {
	live := func(mut func(*Config)) error {
		c := Config{Mode: ModeLive, RazorpayKeyID: "rzp_live_abc", RazorpayKeySecret: "s", RazorpayWebhookSecret: "w", AdminToken: "x", Sandbox: true, PaymentProvider: "sandbox"}
		if mut != nil {
			mut(&c)
		}
		err := c.validateMode()
		if err == nil {
			require.False(t, c.Sandbox, "live mode always disables sandbox endpoints")
			require.Equal(t, "razorpay", c.PaymentProvider)
		}
		return err
	}
	require.NoError(t, live(nil))
	require.Error(t, live(func(c *Config) { c.RazorpayKeyID = "rzp_test_abc" }), "test keys never run live")
	require.Error(t, live(func(c *Config) { c.RazorpayKeyID = "" }))
	require.Error(t, live(func(c *Config) { c.RazorpayWebhookSecret = "" }))
	require.Error(t, live(func(c *Config) { c.AdminToken = "" }))

	test := Config{Mode: ModeTest, RazorpayKeyID: "rzp_live_abc"}
	require.Error(t, test.validateMode(), "live keys never run in test mode")
	require.NoError(t, (&Config{Mode: ModeTest}).validateMode())
	require.NoError(t, (&Config{Mode: ModeTest, RazorpayKeyID: "rzp_test_x"}).validateMode())
	require.Error(t, (&Config{Mode: "staging"}).validateMode())
}

func TestEnsureModeRefusesTheOtherMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()
	pool, cleanup, err := testutil.StartPostgres(ctx)
	require.NoError(t, err)
	defer cleanup()

	require.NoError(t, EnsureMode(ctx, pool, ModeLive), "first start stamps the database")
	require.NoError(t, EnsureMode(ctx, pool, ModeLive), "same mode restarts fine")
	require.ErrorContains(t, EnsureMode(ctx, pool, ModeTest), "refusing to start in test mode")
	_, err = pool.Exec(ctx, `UPDATE instance_settings SET mode = 'test'`)
	require.Error(t, err, "the stamp cannot be edited")
}
