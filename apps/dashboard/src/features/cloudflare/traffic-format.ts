const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const;

export function formatTrafficBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  let value = bytes;
  let index = 0;
  while (value >= 1024 && index < UNITS.length - 1) {
    value /= 1024;
    index++;
  }
  if (index === 0) return `${Math.round(value).toLocaleString()} B`;
  const precision = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(precision)} ${UNITS[index]}`;
}

export function cacheHitRate(requests: number, cachedRequests: number): string {
  if (!Number.isFinite(requests) || requests <= 0) return '0%';
  const bounded = Math.max(0, Math.min(cachedRequests, requests));
  const percent = (bounded / requests) * 100;
  return `${percent >= 10 || percent === 0 ? Math.round(percent) : percent.toFixed(1)}%`;
}
