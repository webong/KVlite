// Minimal Node.js 18+ client for `kvlite serve` (no dependencies).
const baseURL = process.env.KVLITE_URL ?? "http://127.0.0.1:8089";
const token = process.env.KVLITE_TOKEN ?? "";

function keyURL(key) {
  return `${baseURL}/v1/entries/${Buffer.from(key).toString("base64url")}`;
}

async function request(method, key, value, ttlSeconds = 0) {
  const url = new URL(keyURL(key));
  if (ttlSeconds) url.searchParams.set("ttl_seconds", ttlSeconds);
  const headers = { Accept: "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;
  if (value !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(url, {
    method,
    headers,
    body: value === undefined ? undefined : JSON.stringify(value),
  });
  if (!response.ok) throw new Error(`${response.status} ${await response.text()}`);
  return response.status === 204 ? undefined : response.json();
}

await request("PUT", "user:101", { id: 101, name: "Ada" }, 3600);
console.log(await request("GET", "user:101"));
