// SPDX-License-Identifier: AGPL-3.0-only
// Adapted from iyear/tdl v0.20.4, app/login/qr.go and pkg/tclient/app.go.
// Upstream revision: 9d7d49eef2bcb04da720c26e33598c49c68b9ddd.
package tdllogin

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/skip2/go-qrcode"
)

const UpstreamVersion = "v0.20.4"

type Credentials struct {
	ID   int
	Hash string
}

// Builtin preserves tdl's own published application identity, not Desktop's.
func Builtin() Credentials {
	return Credentials{ID: 15055931, Hash: "021d433426cbb920eeb95164498fe3d3"}
}

type QRFlow interface {
	Auth(context.Context, qrlogin.LoggedIn, func(context.Context, qrlogin.Token) error, ...int64) (*tg.AuthAuthorization, error)
}

// QRLogin keeps all rendering and two-factor prompts on the caller's terminal.
func QRLogin(ctx context.Context, client *telegram.Client, loggedIn qrlogin.LoggedIn, prompter auth.UserAuthenticator, output io.Writer) error {
	return loginQR(ctx, client.QR(), loggedIn, prompter.Password, func(ctx context.Context, password string) error {
		_, err := client.Auth().Password(ctx, password)
		return err
	}, output)
}

func loginQR(ctx context.Context, flow QRFlow, loggedIn qrlogin.LoggedIn, prompt func(context.Context) (string, error), authenticate func(context.Context, string) error, output io.Writer) error {
	_, err := flow.Auth(ctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
		code, err := qrcode.New(token.URL(), qrcode.Medium)
		if err != nil {
			return fmt.Errorf("generate login QR: %w", err)
		}
		if _, err := fmt.Fprintf(output, "Scan in Telegram: Settings > Devices > Link Desktop Device (expires %s)\n%s", token.Expires().Format(time.RFC3339), code.ToSmallString(false)); err != nil {
			return err
		}
		return nil
	})
	if !tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return err
	}
	password, err := prompt(ctx)
	if err != nil {
		return err
	}
	return authenticate(ctx, password)
}
