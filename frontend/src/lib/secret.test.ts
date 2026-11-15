import { describe, expect, it } from "vitest";
import { buildSecretBody, isValidOnePasswordRef, isValidSecretName, secretRequestPath, validateSecretForm, type SecretFormInput } from "./secret";

const base: SecretFormInput = {
  name: "OPENAI_API_KEY",
  description: " embeddings ",
  mode: "value",
  value: "v",
  ref: "",
  backend: "",
  project: "",
};

describe("secret helpers", () => {
  it("validates names", () => {
    expect(isValidSecretName("OPENAI_API_KEY")).toBe(true);
    expect(isValidSecretName("_X1")).toBe(true);
    expect(isValidSecretName("1X")).toBe(false);
    expect(isValidSecretName("lower")).toBe(false);
    expect(isValidSecretName("")).toBe(false);
  });

  it("validates 1Password refs", () => {
    expect(isValidOnePasswordRef("op://Private/OpenAI/credential")).toBe(true);
    expect(isValidOnePasswordRef("op://Private/OpenAI")).toBe(false);
    expect(isValidOnePasswordRef("https://x")).toBe(false);
  });

  it("validates the form", () => {
    expect(validateSecretForm(base)).toBeNull();
    expect(validateSecretForm({ ...base, value: "" })).not.toBeNull();
    expect(validateSecretForm({ ...base, mode: "1password", ref: "op://a/b/c" })).toBeNull();
    expect(validateSecretForm({ ...base, mode: "1password", ref: "x" })).not.toBeNull();
  });

  it("builds a value body", () => {
    expect(buildSecretBody({ ...base, backend: "file", project: "/p/a" })).toEqual({
      name: "OPENAI_API_KEY",
      description: "embeddings",
      backend: "file",
      value: "v",
      allowedProjects: ["/p/a"],
    });
    expect(buildSecretBody(base)).toEqual({ name: "OPENAI_API_KEY", description: "embeddings", value: "v" });
  });

  it("builds a 1Password body without a value", () => {
    const body = buildSecretBody({ ...base, mode: "1password", ref: " op://a/b/c ", value: "leftover" });
    expect(body).toEqual({ name: "OPENAI_API_KEY", description: "embeddings", backend: "1password", ref: "op://a/b/c" });
  });

  it("request path carries only IDs", () => {
    expect(secretRequestPath("e1", "s1")).toBe("/secrets/new?escalation=e1&session=s1");
  });
});
