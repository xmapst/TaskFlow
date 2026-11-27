const inputChunkBytes = 32 * 1024

export function* terminalInputChunks(data: Uint8Array<ArrayBuffer>, utf8: boolean) {
  for (let offset = 0; offset < data.length;) {
    let end = Math.min(offset + inputChunkBytes, data.length)
    if (end < data.length) {
      let boundary = end
      // A leading NUL denotes a file-transfer message. Prefer a boundary
      // that preserves that distinction and does not bisect a UTF-8 rune.
      while (boundary > offset && (data[boundary] === 0 ||
        (utf8 && (data[boundary]! & 0xc0) === 0x80))) boundary--
      if (boundary > offset) end = boundary
    }
    yield data.subarray(offset, end)
    offset = end
  }
}
