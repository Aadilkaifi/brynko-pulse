#!/bin/sh
# Copyright 2026 Brynko Technologies
# SPDX-License-Identifier: AGPL-3.0-only
# Removes the service when the package is removed, not when it is upgraded.
# deb passes "remove" or "upgrade"; rpm passes 0 (remove) or 1 (upgrade).
# The configuration and history are kept; pulse uninstall --purge deletes them.
set -e
case "$1" in
	remove | 0)
		/usr/bin/pulse uninstall || true
		;;
esac
