"""Shared live WebSocket checks for the adapter and management listeners."""

UPGRADE = {'Connection': 'Upgrade', 'Upgrade': 'websocket',
           'Sec-WebSocket-Version': '13', 'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ=='}


def mock_requests(endpoint):
    # Neither case sends Origin: non-browser clients must obey the same boundary.
    return ((endpoint + '?mock=true', dict(UPGRADE)),
            (endpoint, {**UPGRADE, 'X-Mock-Adapter': 'true'}))


def check_real_websocket_auth(read, endpoint, token=None, required=False):
    """Check real ingress without Origin; only inbound Bearer credentials count."""
    status = 401 if token is not None or required else 101
    result = read(endpoint, headers=UPGRADE)
    assert result['status'] == status, (endpoint, 'real adapter missing token', result['status'])
    if token is not None or required:
        for value in ('synthetic-wrong-token', 'synthetic-control-token'):
            result = read(endpoint, headers={**UPGRADE, 'Authorization': 'Bearer ' + value})
            assert result['status'] == 401, (endpoint, 'real adapter incorrect token', result['status'])
    if token is not None:
        result = read(endpoint, headers={**UPGRADE, 'Authorization': 'Bearer ' + token})
        assert result['status'] == 101, (endpoint, 'real adapter valid token', result['status'])


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
