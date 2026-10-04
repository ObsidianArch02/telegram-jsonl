// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMaskedSecret(t *testing.T) {
	for _, tt := range []struct {
		name, input, value, echo string
	}{
		{"phone", "+123456789\r", "+123456789", "**********"},
		{"code", "12345\n", "12345", "*****"},
		{"pasted phone", "\x1b[200~+123456789\x1b[201~\r", "+123456789", "**********"},
		{"pasted code", "\x1b[200~12345\x1b[201~\r", "12345", "*****"},
		{"backspace", "12\x7f3\r", "13", "**\b \b*"},
		{"empty backspace", "\b4\r", "4", "*"},
		{"clear line", "12\x153\r", "3", "**\b \b\b \b*"},
		{"cursor keys", "1\x1b[D\x1bOC2\r", "12", "**"},
		{"unicode", "\u00e9\x7f\u754c\r", "\u754c", "*\b \b*"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			value, err := readMaskedSecret(context.Background(), strings.NewReader(tt.input), &output)
			if err != nil || value != tt.value || output.String() != tt.echo {
				t.Fatal("masked input or echo did not match expectations")
			}
		})
	}
}

func TestMaskedSecretInterrupted(t *testing.T) {
	for _, tt := range []struct {
		input string
		err   error
	}{
		{"12\x03", context.Canceled},
		{"12\x04", io.EOF},
		{"12", io.EOF},
	} {
		var output bytes.Buffer
		value, err := readMaskedSecret(context.Background(), strings.NewReader(tt.input), &output)
		if value != "" || !errors.Is(err, tt.err) || output.String() != "**" {
			t.Fatal("interrupted input leaked content or returned an unexpected error")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	if _, err := readMaskedSecret(ctx, strings.NewReader("123\r"), &output); !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatal("cancelled input was read or echoed")
	}
}
