// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import "github.com/radioBros/SimpleTickets/internal/server"

// mountDirectory serves the directory routes owned by this module.
func (m *Module) mountDirectory(g server.Groups) { m.dir.Mount(g) }
