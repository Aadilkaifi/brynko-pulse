#!/bin/sh
# Copyright 2026 Brynko Technologies
# SPDX-License-Identifier: AGPL-3.0-only
# Sets pulse up as a service after the package is installed or upgraded.
# A failure here must not fail the package, so the program stays installed.
set -e
if [ -d /run/systemd/system ]; then
	/usr/bin/pulse install || echo "brynko-pulse: the service was not started. Fix the problem above, then run: sudo pulse install" >&2
else
	echo "brynko-pulse: systemd was not found, so no service was set up. Start monitoring with: pulse run" >&2
fi
