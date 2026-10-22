// SPDX-License-Identifier: AGPL-3.0-only

package crypto

import "testing"

// TestEncryptDecryptRoundTrip (PLT-019 name) — see TestSealOpenRoundTrip.
func TestEncryptDecryptRoundTrip(t *testing.T) { TestSealOpenRoundTrip(t) }
