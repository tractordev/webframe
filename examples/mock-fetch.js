// Inject with -i to replace fetch with an in-memory /api/todos, so a page
// can be tested without a server. Requests are recorded in window.requests.
// Set the starting list with -E TODOS='["a","b"]'.
(function () {
  var store = JSON.parse(webframe.env.TODOS || '["Buy milk"]');
  window.requests = [];
  window.fetch = function (url, opts) {
    var method = (opts && opts.method) || "GET";
    window.requests.push(method + " " + url);
    if (String(url).endsWith("/api/todos")) {
      if (method === "PUT") store = JSON.parse(opts.body);
      return Promise.resolve(new Response(JSON.stringify(store), {
        headers: { "Content-Type": "application/json" }
      }));
    }
    return Promise.resolve(new Response("not found", { status: 404 }));
  };
})();
