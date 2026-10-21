// SPDX-License-Identifier: AGPL-3.0-only

package main

import "testing"

func TestLicenseAllowed(t *testing.T) {
	for expr, want := range map[string]bool{
		"MIT":                     true,
		"(MPL-2.0 OR Apache-2.0)": true,
		"Apache-2.0 AND MIT":      true,
		"GPL-2.0-only OR MIT":     true, // we may pick MIT
		"GPL-2.0-only":            false,
		"SSPL-1.0":                false,
		"BUSL-1.1":                false,
		"CC-BY-NC-4.0":            false,
		"MIT AND SSPL-1.0":        false,
		"UNKNOWN":                 false,
		"":                        false,
	} {
		if got := licenseAllowed(expr); got != want {
			t.Errorf("licenseAllowed(%q) = %v, want %v", expr, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	for text, want := range map[string]string{
		"Permission is hereby granted, free of charge, to any person":                      "MIT",
		"Apache License\n Version 2.0, January 2004":                                       "Apache-2.0",
		"Redistribution and use in source and binary forms ... Neither the name of Google": "BSD-3-Clause",
		"Redistribution and use in source and binary forms, with or without modification":  "BSD-2-Clause",
		"Permission to use, copy, modify, and/or distribute this software for any purpose": "ISC",
		"Mozilla Public License Version 2.0":                                               "MPL-2.0",
		"GNU GENERAL PUBLIC LICENSE Version 2, June 1991":                                  "UNKNOWN",
		"Server Side Public License VERSION 1, OCTOBER 16, 2018":                           "UNKNOWN",
	} {
		if got := classify(text); got != want {
			t.Errorf("classify(%.30q) = %s, want %s", text, got, want)
		}
	}
}
