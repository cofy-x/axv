export function isMarkdownFilename(name: string): boolean {
  return /\.(?:md|markdown)$/i.test(name);
}
