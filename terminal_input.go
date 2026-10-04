// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bufio"
	"context"
	"io"
)

func readMaskedSecret(ctx context.Context, input io.Reader, output io.Writer) (string, error) {
	reader := bufio.NewReader(input)
	var value []rune
	defer func() {
		clear(value)
	}()
	escape := false
	sequence := false
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		r, _, err := reader.ReadRune()
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Ignore terminal key sequences so cursor keys cannot enter a secret.
		if escape {
			escape = false
			sequence = r == '[' || r == 'O'
			continue
		}
		if sequence {
			if r >= 0x40 && r <= 0x7e {
				sequence = false
			}
			continue
		}
		var echo string
		switch r {
		case '\r', '\n':
			return string(value), nil
		case 3:
			return "", context.Canceled
		case 4:
			return "", io.EOF
		case 27:
			escape = true
		case '\b', 127:
			if len(value) > 0 {
				value[len(value)-1] = 0
				value = value[:len(value)-1]
				echo = "\b \b"
			}
		case 21:
			for i := range value {
				value[i] = 0
				echo += "\b \b"
			}
			value = value[:0]
		default:
			if r >= 32 {
				value = append(value, r)
				echo = "*"
			}
		}
		if echo != "" {
			if _, err := io.WriteString(output, echo); err != nil {
				return "", err
			}
		}
	}
}
