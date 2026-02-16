"""Tests for run_ports (find_free_port)."""

import pytest

from octopilot_pipeline_tools.run_ports import find_free_port


def test_find_free_port_returns_port_in_range() -> None:
    port = find_free_port(start=8080, max_tries=5)
    assert 8080 <= port < 8080 + 5


def test_find_free_port_different_ports_when_first_busy() -> None:
    """When start port is in use, find_free_port returns next available."""
    import socket

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 9990))
        port = find_free_port(start=9990, max_tries=10)
        assert port != 9990
        assert 9991 <= port < 10000


def test_find_free_port_raises_when_all_busy() -> None:
    """When all ports in range are busy, OSError is raised."""
    import socket

    sockets = []
    try:
        for i in range(5):
            s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            s.bind(("127.0.0.1", 9000 + i))
            sockets.append(s)
        with pytest.raises(OSError, match="No free port"):
            find_free_port(start=9000, max_tries=5)
    finally:
        for s in sockets:
            s.close()
