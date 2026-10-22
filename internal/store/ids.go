// SPDX-License-Identifier: AGPL-3.0-only

package store

import "github.com/google/uuid"

// NewID returns a UUIDv7 string: time-ordered, so ticket_events IDs double as
// the dispatcher cursor and the WebSocket resume position (D-11, realtime spec §5).
func NewID() string { return uuid.Must(uuid.NewV7()).String() }
