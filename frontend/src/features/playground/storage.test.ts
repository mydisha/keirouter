import { afterEach, describe, expect, it, vi } from "vitest";
import { MAX_CONVERSATIONS, loadPlayground, mergeConversations, savePlayground } from "./storage";
import type { Conversation } from "./types";

function memoryStorage(limit = Infinity) {
  const items = new Map<string, string>();

  return {
    items,
    getItem: (k: string) => items.get(k) ?? null,
    setItem: (k: string, v: string) => {
      if (v.length > limit) {
        throw new DOMException("full", "QuotaExceededError");
      }
      items.set(k, v);
    },
  };
}

const conversation = (id: string, updatedAt: number): Conversation => ({
  id,
  title: `Chat ${id}`,
  model: "openai/o3",
  effort: "medium",
  system: "",
  updatedAt,
  messages: [
    { id: `${id}-u`, role: "user", content: "Hi ✓" },
    {
      id: `${id}-a`,
      role: "assistant",
      model: "openai/o3",
      served: null,
      effort: "medium",
      content: "Hel",
      reasoning: "",
      status: "streaming",
      error: null,
      usage: null,
      requestId: null,
      latencyMs: null,
      thinkingMs: null,
    },
  ],
});

afterEach(() => vi.unstubAllGlobals());

describe("playground storage", () => {
  it("round-trips per workspace and saves a streaming reply as stopped", () => {
    const storage = memoryStorage();
    vi.stubGlobal("window", { localStorage: storage });

    savePlayground("acme", {
      conversations: [conversation("a", 1)],
      lastModel: "openai/o3",
      lastEffort: "high",
    });
    const loaded = loadPlayground("acme");

    expect(loaded.lastModel).toBe("openai/o3");
    expect(loaded.lastEffort).toBe("high");
    expect(loaded.conversations[0].messages[1]).toMatchObject({
      status: "stopped",
    });
    expect(loadPlayground("other").conversations).toEqual([]);
  });

  it("keeps the most recent chats", () => {
    vi.stubGlobal("window", { localStorage: memoryStorage() });
    const many = Array.from({ length: MAX_CONVERSATIONS + 5 }, (_, i) => conversation(String(i), i));

    savePlayground("acme", {
      conversations: many,
      lastModel: null,
      lastEffort: "medium",
    });
    const ids = loadPlayground("acme").conversations.map((c) => c.id);

    expect(ids).toHaveLength(MAX_CONVERSATIONS);
    expect(ids[0]).toBe(String(MAX_CONVERSATIONS + 4));
  });

  it("drops the oldest chats when storage is full", () => {
    const one = JSON.stringify({
      conversations: [conversation("new", 2)],
      lastModel: null,
      lastEffort: "medium",
    }).length;
    vi.stubGlobal("window", { localStorage: memoryStorage(one) });

    savePlayground("acme", {
      conversations: [conversation("old", 1), conversation("new", 2)],
      lastModel: null,
      lastEffort: "medium",
    });

    expect(loadPlayground("acme").conversations.map((c) => c.id)).toEqual(["new"]);
  });

  it("starts empty on corrupt data, foreign shapes or blocked storage", () => {
    const storage = memoryStorage();
    storage.items.set("kr.playground.v1.acme", "{not json");
    vi.stubGlobal("window", { localStorage: storage });
    expect(loadPlayground("acme").conversations).toEqual([]);

    storage.items.set(
      "kr.playground.v1.acme",
      JSON.stringify({
        conversations: [{ id: 1 }, conversation("ok", 1)],
        lastEffort: "max",
      }),
    );
    const loaded = loadPlayground("acme");
    expect(loaded.conversations.map((c) => c.id)).toEqual(["ok"]);
    expect(loaded.lastEffort).toBe("medium");

    vi.stubGlobal("window", {
      get localStorage(): Storage {
        throw new DOMException("denied", "SecurityError");
      },
    });
    expect(loadPlayground("acme").conversations).toEqual([]);
    expect(() =>
      savePlayground("acme", {
        conversations: [],
        lastModel: null,
        lastEffort: "medium",
      }),
    ).not.toThrow();
  });
});

describe("mergeConversations", () => {
  it("keeps attached image references and drops chats with malformed ones", () => {
    const storage = memoryStorage();
    vi.stubGlobal("window", { localStorage: storage });
    const image = {
      id: "img1",
      name: "café.png",
      mime: "image/png",
      width: 640,
      height: 480,
      bytes: 12_345,
    };
    const withImage = conversation("a", 2);
    withImage.messages[0] = {
      id: "a-u",
      role: "user",
      content: "",
      images: [image],
    };
    const broken = conversation("b", 1);
    broken.messages[0] = {
      id: "b-u",
      role: "user",
      content: "x",
      images: [{ id: "img2" }] as never,
    };

    savePlayground("acme", {
      conversations: [withImage, broken],
      lastModel: null,
      lastEffort: "medium",
    });
    const loaded = loadPlayground("acme").conversations;

    expect(loaded.map((c) => c.id)).toEqual(["a"]);
    expect(loaded[0].messages[0]).toEqual({
      id: "a-u",
      role: "user",
      content: "",
      images: [image],
    });
  });

  it("keeps chats from both tabs and the newer copy of each", () => {
    const mine = [conversation("a", 5), conversation("b", 1)];
    const theirs = [conversation("b", 3), conversation("c", 2), conversation("a", 4)];

    const merged = mergeConversations(mine, theirs);

    expect(merged.map((c) => [c.id, c.updatedAt]).sort((x, y) => String(x[0]).localeCompare(String(y[0])))).toEqual([
      ["a", 5],
      ["b", 3],
      ["c", 2],
    ]);
  });
});
