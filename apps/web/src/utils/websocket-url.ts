const wildcardHosts = new Set(['', '0.0.0.0', '::', '[::]']);

function pageHostname(pageOrigin: string): string {
  try {
    return new URL(pageOrigin).hostname || '127.0.0.1';
  } catch {
    return '127.0.0.1';
  }
}

export function instanceWebSocketURL(
  listenAddress: string,
  instanceID: string,
  adapter: 'onebot' | 'astrbot',
  pageOrigin: string,
): string {
  const configured = listenAddress.trim() || '127.0.0.1:1234';
  const explicitScheme = /^wss?:\/\//i.test(configured);
  const parsed = new URL(
    explicitScheme
      ? configured
      : `ws://${configured.startsWith(':') ? `0.0.0.0${configured}` : configured}`,
  );
  let host = parsed.hostname;
  if (wildcardHosts.has(host)) host = pageHostname(pageOrigin);
  if (host.includes(':') && !host.startsWith('[')) host = `[${host}]`;
  const scheme = explicitScheme ? parsed.protocol : 'ws:';
  const authority = `${host}${parsed.port ? `:${parsed.port}` : ''}`;
  return `${scheme}//${authority}/instances/${instanceID}/ws/${adapter}`;
}
