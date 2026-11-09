// crypto.randomUUID needs a secure context; local .test sites are plain http.
export const newId = () =>
  Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, "0")).join("");
