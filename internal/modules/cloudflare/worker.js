// The Worker of Just Enough Certs (JEC): it answers the requests for the
// files of domain validations, which JEC keeps in Workers KV under the host
// and path they are for, and passes every other request on to the origin.
// JEC deploys it, and replaces it with its own code each time it sets up;
// changes made here are lost.
export default {
  async fetch(request, env) {
    if (request.method === "GET" || request.method === "HEAD") {
      const url = new URL(request.url);
      let value = null;
      try {
        value = await env.FILES.get(url.hostname + url.pathname, { type: "arrayBuffer" });
      } catch {
        value = null; // the origin answers instead
      }
      if (value !== null) {
        return new Response(request.method === "HEAD" ? null : value, {
          headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" },
        });
      }
    }
    return fetch(request);
  },
};
