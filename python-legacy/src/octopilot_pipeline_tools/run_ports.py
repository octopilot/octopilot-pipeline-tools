"""Helpers for op run port mapping (free port on localhost)."""

from __future__ import annotations

import socket


def find_free_port(start: int = 8080, max_tries: int = 100) -> int:
    """
    Find an available port on 127.0.0.1 by attempting to bind.
    Tries start, start+1, ... up to max_tries. Raises OSError if none free.
    """
    for i in range(max_tries):
        port = start + i
        try:
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                s.bind(("127.0.0.1", port))
                return port
        except OSError:
            continue
    raise OSError(f"No free port in range [{start}, {start + max_tries - 1}]")
