/**
 * How a reply comes back: free text, any JSON object, or JSON that follows a
 * schema. The request carries `response_format`, which providers with a JSON
 * mode enforce, and the system prompt asks for JSON too (withJsonInstruction).
 * The schema stays text so a half-typed edit survives a reload.
 */
export type OutputFormat = { mode: "text" } | { mode: "json" } | { mode: "schema"; name: string; schema: string };

export const TEXT_OUTPUT: OutputFormat = { mode: "text" };

const NAME = /^[A-Za-z0-9_-]{1,64}$/;

const nullable = (type: string) => ({ type: [type, "null"] });
const object = (properties: Record<string, unknown>) => ({
  type: "object",
  additionalProperties: false,
  required: Object.keys(properties),
  properties,
});
const list = (items: unknown) => ({ type: "array", items });

/** Starting points for pulling fields out of a document; strict-mode ready. */
export const OUTPUT_PRESETS: { label: string; name: string; schema: object }[] = [
  {
    label: "Invoice",
    name: "invoice",
    schema: object({
      invoice_number: { type: "string" },
      issue_date: nullable("string"),
      due_date: nullable("string"),
      seller: { type: "string" },
      buyer: { type: "string" },
      currency: { type: "string" },
      line_items: list(
        object({
          description: { type: "string" },
          quantity: { type: "number" },
          unit_price: { type: "number" },
          amount: { type: "number" },
        }),
      ),
      subtotal: nullable("number"),
      tax: nullable("number"),
      total: { type: "number" },
    }),
  },
  {
    label: "Receipt",
    name: "receipt",
    schema: object({
      merchant: { type: "string" },
      date: nullable("string"),
      items: list(
        object({
          name: { type: "string" },
          quantity: { type: "number" },
          price: { type: "number" },
        }),
      ),
      total: { type: "number" },
      payment_method: nullable("string"),
    }),
  },
  {
    label: "Resume",
    name: "resume",
    schema: object({
      name: { type: "string" },
      email: nullable("string"),
      phone: nullable("string"),
      summary: { type: "string" },
      experience: list(
        object({
          company: { type: "string" },
          title: { type: "string" },
          start: nullable("string"),
          end: nullable("string"),
        }),
      ),
      education: list(
        object({
          school: { type: "string" },
          degree: nullable("string"),
          year: nullable("string"),
        }),
      ),
      skills: list({ type: "string" }),
    }),
  },
  {
    label: "Table",
    name: "table",
    schema: object({
      columns: list({ type: "string" }),
      rows: list(list({ type: "string" })),
    }),
  },
];

export function presetOutput(preset: (typeof OUTPUT_PRESETS)[number]): Extract<OutputFormat, { mode: "schema" }> {
  return {
    mode: "schema",
    name: preset.name,
    schema: JSON.stringify(preset.schema, null, 2),
  };
}

/** What is wrong with a schema output, or null when it can be sent. */
export function schemaError(output: OutputFormat): string | null {
  if (output.mode !== "schema") {
    return null;
  }
  if (!NAME.test(output.name)) {
    return "Name the schema with letters, digits, _ or -, up to 64 characters.";
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(output.schema);
  } catch {
    return "The schema is not valid JSON.";
  }
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    Array.isArray(parsed) ||
    (parsed as { type?: unknown }).type !== "object"
  ) {
    return 'The schema must describe an object ("type": "object").';
  }

  return null;
}

/** The request's `response_format`, or null for text (and a schema with errors). */
export function responseFormat(output: OutputFormat): object | null {
  switch (output.mode) {
    case "text":
      return null;
    case "json":
      return { type: "json_object" };
    case "schema":
      return schemaError(output) === null
        ? {
            type: "json_schema",
            json_schema: {
              name: output.name,
              strict: true,
              schema: JSON.parse(output.schema),
            },
          }
        : null;
  }
}

/**
 * JSON mode on OpenAI-style providers refuses a request whose messages never
 * mention JSON, so the system prompt says it.
 */
export function withJsonInstruction(system: string, output: OutputFormat): string {
  if (output.mode === "text") {
    return system;
  }
  const ask = "Reply with one JSON object and nothing else.";

  return system.trim() ? `${system.trim()}\n\n${ask}` : ask;
}

/** The reply as indented JSON, or null when it is not JSON. Tolerates a ```json fence. */
export function prettyJson(content: string): string | null {
  const body = content
    .trim()
    .replace(/^```(?:json)?\s*\n/i, "")
    .replace(/\n?```$/, "");
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return null;
  }
}

export function outputLabel(output: OutputFormat): string {
  switch (output.mode) {
    case "text":
      return "Text";
    case "json":
      return "JSON";
    case "schema":
      return output.name || "Schema";
  }
}

export function isOutputFormat(value: unknown): value is OutputFormat {
  const o = value as OutputFormat;
  if (typeof o !== "object" || o === null) {
    return false;
  }

  return (
    o.mode === "text" ||
    o.mode === "json" ||
    (o.mode === "schema" && typeof o.name === "string" && typeof o.schema === "string")
  );
}
