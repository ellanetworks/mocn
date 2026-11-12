import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiFetch, withQuery } from "@/queries/utils";

const stubFetch = (status: number, body: string, statusText = "") => {
  const fetchMock = vi.fn(
    async () => new Response(body, { status, statusText }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("apiFetch", () => {
  it("unwraps the result", async () => {
    stubFetch(200, JSON.stringify({ result: { id: 1 } }));

    await expect(apiFetch("/api/v1/users/me")).resolves.toEqual({ id: 1 });
  });

  it("sends a JSON body", async () => {
    const fetchMock = stubFetch(201, JSON.stringify({ result: {} }));

    await apiFetch("/api/v1/users", {
      method: "POST",
      body: { a: 1 },
    });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/users", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"a":1}',
    });
  });

  it("surfaces the server error message", async () => {
    stubFetch(400, JSON.stringify({ error: "invalid email" }));

    const err = await apiFetch("/api/v1/users/me").catch((e) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({
      kind: "http",
      status: 400,
      message: "invalid email",
    });
  });

  it("falls back to the status text when the error body is not JSON", async () => {
    stubFetch(404, "404 page not found", "Not Found");

    await expect(apiFetch("/nope")).rejects.toMatchObject({
      kind: "http",
      status: 404,
      message: "Not Found",
    });
  });

  it("rejects a successful response that is not JSON", async () => {
    stubFetch(200, "<html></html>");

    await expect(apiFetch("/api/v1/status")).rejects.toMatchObject({
      kind: "http",
      status: 200,
    });
  });

  it("reports network failures", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );

    await expect(apiFetch("/api/v1/status")).rejects.toMatchObject({
      kind: "network",
      status: undefined,
    });
  });
});

describe("withQuery", () => {
  it("drops empty values", () => {
    expect(
      withQuery("/api/v1/users", {
        page: 2,
        search: "",
        per_page: undefined,
      }),
    ).toBe("/api/v1/users?page=2");
  });

  it("returns the bare path without parameters", () => {
    expect(withQuery("/api/v1/users", {})).toBe("/api/v1/users");
  });

  it("repeats the values of a list", () => {
    expect(
      withQuery("/api/v1/users", {
        role: ["1", "2"],
        page: 1,
      }),
    ).toBe("/api/v1/users?role=1&role=2&page=1");
  });

  it("encodes the plus sign", () => {
    expect(withQuery("/api/v1/users", { search: "a+b@example.com" })).toBe(
      "/api/v1/users?search=a%2Bb%40example.com",
    );
  });
});
