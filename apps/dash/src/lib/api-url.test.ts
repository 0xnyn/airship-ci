import assert from "node:assert/strict";
import { test } from "node:test";

import { isLocalApiUrl } from "./api-url.ts";

test("local API URLs are recognised", () => {
  for (const local of [
    "http://localhost:8080",
    "http://localhost",
    "https://127.0.0.1:8443/",
    "http://[::1]:8080",
  ]) {
    assert.equal(isLocalApiUrl(local), true, local);
  }
  for (const remote of [
    "https://api.example.com",
    "https://localhost.example.com",
    "http://10.0.0.5:8080",
  ]) {
    assert.equal(isLocalApiUrl(remote), false, remote);
  }
});
