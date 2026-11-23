export function projectFileLocation(file = '') {
  return {
    folder: file.split('/').slice(0, -1).join('/'),
    file,
    cursor: '',
    reading: !!file,
  };
}
