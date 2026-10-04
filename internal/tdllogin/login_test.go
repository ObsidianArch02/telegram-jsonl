// SPDX-License-Identifier: AGPL-3.0-only
package tdllogin

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type fakeQR struct{ err error }

func (f fakeQR) Auth(ctx context.Context, _ qrlogin.LoggedIn, show func(context.Context, qrlogin.Token) error, _ ...int64) (*tg.AuthAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := show(ctx, qrlogin.NewToken([]byte("temporary-login-token"), int(time.Now().Add(time.Minute).Unix()))); err != nil {
		return nil, err
	}
	return &tg.AuthAuthorization{}, f.err
}

func TestQRSourceFlowHandlesTwoFactorWithoutPrintingPassword(t *testing.T) {
	var output bytes.Buffer
	prompted, authenticated := false, false
	err := loginQR(context.Background(), fakeQR{err: tgerr.New(401, "SESSION_PASSWORD_NEEDED")}, nil, func(context.Context) (string, error) { prompted = true; return "private-password", nil }, func(_ context.Context, password string) error {
		authenticated = password == "private-password"
		return nil
	}, &output)
	if err != nil || !prompted || !authenticated {
		t.Fatal("two-factor QR flow failed")
	}
	if strings.Contains(output.String(), "private-password") || !strings.Contains(output.String(), "Scan in Telegram") {
		t.Fatal("login output exposed password or omitted QR prompt")
	}
}

func TestQRSourceFlowPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err := loginQR(ctx, fakeQR{}, nil, func(context.Context) (string, error) { t.Fatal("prompt after cancellation"); return "", nil }, func(context.Context, string) error { return nil }, &output)
	if !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatal("cancellation lost")
	}
}

func TestQRSourceFlowDoesNotPromptOnSuccessfulExistingApproval(t *testing.T) {
	var output bytes.Buffer
	err := loginQR(context.Background(), fakeQR{}, nil, func(context.Context) (string, error) { t.Fatal("unnecessary two-factor prompt"); return "", nil }, func(context.Context, string) error { t.Fatal("unnecessary password submission"); return nil }, &output)
	if err != nil {
		t.Fatal(err)
	}
}
