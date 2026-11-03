"""Shared live WebSocket checks for the adapter and management listeners."""

UPGRADE = {'Connection': 'Upgrade', 'Upgrade': 'websocket',
           'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ=='}


def mock_requests(endpoint):
    # Neither case sends Origin: non-browser clients must obey the same boundary.
    return ((endpoint + '?mock=true', dict(UPGRADE)),
            (endpoint, {**UPGRADE, 'X-Mock-Adapter': 'true'}))


def check_adapter_websocket(read, endpoint):
    """Require a working real adapter, but reject both mock markers."""
    result = read(endpoint, headers=UPGRADE)
    assert result['status'] == 101, (endpoint, 'ordinary adapter', result)
    for path, headers in mock_requests(endpoint):
        for origin in ({}, {'Origin': 'http://127.0.0.1:8080'}):
            result = read(path, headers={**headers, **origin})
            assert result['status'] == 403, (path, 'adapter must reject mock', result)


def check_management_websocket(read, endpoint, token=None):
    """Keep Direct Chat usable under the existing management token policy."""
    for path, headers in mock_requests(endpoint):
        if token is not None:
            for credentials in ({}, {'Authorization': 'Bearer synthetic-incorrect-token'}):
                result = read(path, headers={**headers, **credentials})
                assert result['status'] == 401, (path, 'management token required', result)
            headers = {**headers, 'Authorization': 'Bearer ' + token}
        result = read(path, headers=headers)
        assert result['status'] == 101, (path, 'management Direct Chat', result)
