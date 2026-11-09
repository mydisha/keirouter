import { describe, expect, it } from "vitest";
import {
  OUTPUT_PRESETS,
  isOutputFormat,
  outputLabel,
  presetOutput,
  prettyJson,
  responseFormat,
  schemaError,
  withJsonInstruction,
} from "./output-format";

const schema = (text: string, name = "invoice") => ({ mode: "schema", name, schema: text }) as const;

describe("output format", () => {
  it("sends nothing for text, json_object for JSON and a strict schema", () => {
    expect(responseFormat({ mode: "text" })).toBeNull();
    expect(responseFormat({ mode: "json" })).toEqual({
      type: "json_object",
    });
    expect(responseFormat(schema('{"type":"object"}'))).toEqual({
      type: "json_schema",
      json_schema: {
        name: "invoice",
        strict: true,
        schema: { type: "object" },
      },
    });
  });

  it("holds back a schema with errors and says what is wrong", () => {
    expect(schemaError(schema('{"type":'))).toBe("The schema is not valid JSON.");
    expect(schemaError(schema('{"type":"array"}'))).toBe('The schema must describe an object ("type": "object").');
    expect(schemaError(schema('{"type":"object"}', "my invoice"))).toBe(
      "Name the schema with letters, digits, _ or -, up to 64 characters.",
    );
    expect(responseFormat(schema("[]"))).toBeNull();
    expect(schemaError({ mode: "json" })).toBeNull();
  });

  it("ships presets every strict provider accepts", () => {
    const check = (node: Record<string, unknown>): void => {
      if (node.type === "object") {
        const props = node.properties as Record<string, object>;
        expect(node.additionalProperties).toBe(false);
        expect(node.required).toEqual(Object.keys(props));
        Object.values(props).forEach((p) => check(p as Record<string, unknown>));
      }
      if (node.type === "array") {
        check(node.items as Record<string, unknown>);
      }
    };
    for (const preset of OUTPUT_PRESETS) {
      const output = presetOutput(preset);
      expect(schemaError(output)).toBeNull();
      check(preset.schema as Record<string, unknown>);
    }
  });

  it("asks for JSON in the system prompt when JSON is wanted", () => {
    expect(withJsonInstruction("Be brief.", { mode: "text" })).toBe("Be brief.");
    expect(withJsonInstruction(" Be brief. ", { mode: "json" })).toBe(
      "Be brief.\n\nReply with one JSON object and nothing else.",
    );
    expect(withJsonInstruction("", schema("{}"))).toBe("Reply with one JSON object and nothing else.");
  });

  it("indents a JSON reply, fenced or not, and refuses prose", () => {
    expect(prettyJson('{"a":1}')).toBe('{\n  "a": 1\n}');
    expect(prettyJson('```json\n{"a":1}\n```')).toBe('{\n  "a": 1\n}');
    expect(prettyJson('Here you go: {"a":1}')).toBeNull();
  });

  it("labels the control and validates saved values", () => {
    expect(outputLabel({ mode: "text" })).toBe("Text");
    expect(outputLabel({ mode: "json" })).toBe("JSON");
    expect(outputLabel(schema("{}", "receipt"))).toBe("receipt");
    expect(isOutputFormat({ mode: "json" })).toBe(true);
    expect(isOutputFormat(schema("{}"))).toBe(true);
    expect(isOutputFormat({ mode: "schema", name: "x" })).toBe(false);
    expect(isOutputFormat({ mode: "yaml" })).toBe(false);
    expect(isOutputFormat(null)).toBe(false);
  });
});
