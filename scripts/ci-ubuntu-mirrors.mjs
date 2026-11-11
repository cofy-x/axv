import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const fallbackMirror = 'https://mirrors.edge.kernel.org/ubuntu/';

export function prioritizeUbuntuMirrors(content) {
  let mirrors = 0;
  let hasFallback = false;
  const lines = content.split('\n').map((line) => {
    if (!line.trim() || line.trimStart().startsWith('#')) return line;
    const [source, ...metadata] = line.trim().split(/\s+/);
    const url = new URL(source);
    if (!['http:', 'https:'].includes(url.protocol))
      throw new Error('CI package mirrors must use HTTP or HTTPS.');
    url.protocol = 'https:';
    const isFallback =
      url.href.replace(/\/$/, '') === fallbackMirror.replace(/\/$/, '');
    hasFallback ||= isFallback;
    const priority = isFallback
      ? 30
      : url.hostname === 'azure.archive.ubuntu.com'
        ? 20
        : 10;
    mirrors++;
    return `${url.href}\t${[
      ...metadata.filter((item) => !item.startsWith('priority:')),
      `priority:${priority}`,
    ].join(' ')}`;
  });
  if (!mirrors) throw new Error('The runner supplied no package mirrors.');
  return `${lines.join('\n').trimEnd()}${
    hasFallback ? '' : `\n${fallbackMirror}\tpriority:30`
  }\n`;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const [input, output] = process.argv.slice(2);
    if (!input || !output || process.argv.length !== 4)
      throw new Error('Usage: ci-ubuntu-mirrors.mjs <input> <output>');
    await writeFile(
      output,
      prioritizeUbuntuMirrors(await readFile(input, 'utf8')),
      { flag: 'wx', mode: 0o600 },
    );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
