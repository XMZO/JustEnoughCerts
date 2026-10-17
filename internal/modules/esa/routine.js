// The Edge Routine of Just Enough Certs (JEC): it answers the requests for
// the files of domain validations, which JEC keeps in Edge KV, and passes
// every other request on to the origin. JEC deploys it, and replaces it when
// a new version of JEC changes it; changes made here are lost.
const namespace = "__NAMESPACE__";

// Keys hold letters, digits, "-" and "_" only: "_" escapes the rest of the
// characters of host names and of the paths of validation files.
const escapes = { _: "__", ".": "_d", "/": "_s", "~": "_t" };

function key(url) {
  const k = (url.hostname + url.pathname).replace(/[_./~]/g, (c) => escapes[c]);
  return /^[A-Za-z0-9_-]{1,512}$/.test(k) ? k : null;
}

export default {
  async fetch(request) {
    if (request.method === "GET" || request.method === "HEAD") {
      const k = key(new URL(request.url));
      let value = null;
      if (k !== null) {
        try {
          value = await new EdgeKV({ namespace }).get(k, { type: "arrayBuffer" });
        } catch {
          value = null; // the origin answers instead
        }
      }
      if (value != null) {
        return new Response(request.method === "HEAD" ? null : value, {
          headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
        });
      }
    }
    return fetch(request);
  },
};
