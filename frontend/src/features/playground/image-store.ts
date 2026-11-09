/**
 * Attached images stay in this browser, like the chats that reference them.
 * They live in IndexedDB rather than next to the chats in
 * localStorage, whose few megabytes a handful of images would fill.
 *
 * Where IndexedDB is missing or refuses (some private windows) images are
 * kept in memory for this tab only and read as missing after a reload.
 */
const DB_NAME = "kr.playground.images.v1";
const STORE = "images";
/** Unreferenced images younger than this may belong to another tab's unsent message. */
export const PRUNE_GRACE_MS = 10 * 60 * 1000;

type Row = { id: string; scope: string; blob: Blob; createdAt: number };

const memory = new Map<string, Row>();
const dataUrls = new Map<string, string>();
let opening: Promise<IDBDatabase | null> | null = null;

function request<T>(req: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function database(): Promise<IDBDatabase | null> {
  opening ??= new Promise<IDBDatabase | null>((resolve) => {
    try {
      if (typeof indexedDB === "undefined") {
        resolve(null);

        return;
      }
      const req = indexedDB.open(DB_NAME, 1);
      req.onupgradeneeded = () => {
        const store = req.result.createObjectStore(STORE, {
          keyPath: "id",
        });
        store.createIndex("scope", "scope");
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
      req.onblocked = () => resolve(null);
    } catch {
      resolve(null);
    }
  });

  return opening;
}

async function withStore<T>(mode: IDBTransactionMode, run: (store: IDBObjectStore) => Promise<T>): Promise<T | null> {
  const db = await database();
  if (!db) {
    return null;
  }
  try {
    return await run(db.transaction(STORE, mode).objectStore(STORE));
  } catch {
    return null;
  }
}

export async function putImage(scope: string, id: string, blob: Blob): Promise<void> {
  const row: Row = { id, scope, blob, createdAt: Date.now() };
  memory.set(id, row);
  await withStore("readwrite", (store) => request(store.put(row)));
}

export async function imageBlob(id: string): Promise<Blob | null> {
  const cached = memory.get(id);
  if (cached) {
    return cached.blob;
  }
  const row = await withStore("readonly", (store) => request(store.get(id) as IDBRequest<Row | undefined>));
  if (!row) {
    return null;
  }
  memory.set(id, row);

  return row.blob;
}

async function readAsDataUrl(blob: Blob): Promise<string> {
  const bytes = new Uint8Array(await blob.arrayBuffer());
  let binary = "";
  // Chunked: spreading megabytes into one call overflows the stack.
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }

  return `data:${blob.type};base64,${btoa(binary)}`;
}

/** The image as a `data:` URL for a request body, or null when it is gone. */
export async function imageDataUrl(id: string): Promise<string | null> {
  const cached = dataUrls.get(id);
  if (cached) {
    return cached;
  }
  const blob = await imageBlob(id);
  if (!blob) {
    return null;
  }
  const url = await readAsDataUrl(blob);
  dataUrls.set(id, url);

  return url;
}

/**
 * Drops this scope's images no chat references any more (a deleted chat, one
 * pushed out of the saved thirty, an attachment removed before sending).
 */
export async function pruneImages(scope: string, referenced: ReadonlySet<string>, now = Date.now()): Promise<void> {
  const stale = (row: Row) => row.scope === scope && !referenced.has(row.id) && now - row.createdAt > PRUNE_GRACE_MS;
  for (const row of memory.values()) {
    if (stale(row)) {
      memory.delete(row.id);
      dataUrls.delete(row.id);
    }
  }
  await withStore("readwrite", async (store) => {
    const rows = await request(store.index("scope").getAll(scope) as IDBRequest<Row[]>);
    await Promise.all(
      rows.filter(stale).map((row) => {
        dataUrls.delete(row.id);

        return request(store.delete(row.id));
      }),
    );
  });
}
