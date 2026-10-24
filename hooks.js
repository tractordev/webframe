(function () {
  if (window.__webframe_hooks) return;
  window.__webframe_hooks = true;

  var queue = [];
  var busy = false;

  // Bound calls run on separate goroutines in Go, so messages are sent in
  // batches with at most one in flight to keep them ordered.
  function flush() {
    if (busy || !queue.length) return;
    if (typeof window.__webframe !== "function") {
      busy = true;
      setTimeout(function () { busy = false; flush(); }, 0);
      return;
    }
    var batch = queue;
    queue = [];
    busy = true;
    window.__webframe(batch).then(done, done);
  }

  function done() {
    busy = false;
    flush();
  }

  function send(kind, level, text, id) {
    queue.push({ kind: kind, level: level || "", text: text || "", id: id || 0 });
    flush();
  }

  function indent(stack) {
    return stack.split("\n").filter(Boolean).map(function (l) {
      return /^\s/.test(l) ? l : "    " + l;
    }).join("\n");
  }

  function fmtErr(e) {
    var head = (e.name || "Error") + ": " + e.message;
    var stack = typeof e.stack === "string" ? e.stack.trim() : "";
    if (!stack) return head;
    if (stack.indexOf(head) === 0) return stack;
    return head + "\n" + indent(stack);
  }

  function fmt(v, quote) {
    switch (typeof v) {
      case "string": return quote ? JSON.stringify(v) : v;
      case "undefined": return "undefined";
      case "function": return "[Function: " + (v.name || "(anonymous)") + "]";
      case "symbol": return v.toString();
      case "bigint": return v + "n";
      case "number": case "boolean": return String(v);
    }
    if (v === null) return "null";
    if (v instanceof Error) return fmtErr(v);
    var stack = [];
    try {
      var s = JSON.stringify(v, function (k, x) {
        switch (typeof x) {
          case "function": return "[Function: " + (x.name || "(anonymous)") + "]";
          case "symbol": return x.toString();
          case "bigint": return x + "n";
          case "undefined": return k === "" ? undefined : "undefined";
        }
        if (x === null || typeof x !== "object") return x;
        if (x instanceof Error) return fmtErr(x);
        if (typeof Node !== "undefined" && x instanceof Node) return "<" + x.nodeName.toLowerCase() + ">";
        if (stack.length) stack.splice(stack.indexOf(this) + 1);
        if (stack.indexOf(x) !== -1) return "[Circular]";
        stack.push(x);
        return x;
      });
      return s === undefined ? String(v) : s;
    } catch (e) {
      return String(v);
    }
  }

  function args(a) {
    return Array.prototype.map.call(a, function (x) { return fmt(x); }).join(" ");
  }

  ["log", "info", "warn", "error", "debug", "trace", "dir"].forEach(function (m) {
    var orig = console[m];
    console[m] = function () {
      var text = args(arguments);
      if (m === "trace") text = (text || "Trace") + "\n" + indent((new Error().stack || "").split("\n").slice(1).join("\n"));
      send("console", m, text);
      if (orig) return orig.apply(console, arguments);
    };
  });

  var origAssert = console.assert;
  console.assert = function (cond) {
    if (!cond) {
      var rest = args(Array.prototype.slice.call(arguments, 1));
      send("console", "assert", "Assertion failed" + (rest ? ": " + rest : ""));
    }
    if (origAssert) return origAssert.apply(console, arguments);
  };

  window.addEventListener("error", function (e) {
    var t = e.target;
    if (t && t !== window && t.nodeType === 1) {
      send("exception", "resource", "Failed to load resource: <" + t.nodeName.toLowerCase() + "> " + (t.currentSrc || t.src || t.href || ""));
      return;
    }
    var s = "Uncaught " + (e.error != null ? fmt(e.error) : e.message || "error");
    if (e.filename && !(e.error && e.error.stack)) s += "\n    at " + e.filename + ":" + e.lineno + ":" + e.colno;
    send("exception", "error", s);
  }, true);

  window.addEventListener("unhandledrejection", function (e) {
    send("exception", "rejection", "Unhandled promise rejection: " + fmt(e.reason));
  });

  window.close = function () { send("close"); };

  send("ready");

  // Drops the frames of the eval machinery below the evaluated code.
  function evalErr(e) {
    if (e instanceof Error && typeof e.stack === "string") {
      var lines = e.stack.split("\n");
      for (var i = 0; i < lines.length; i++) {
        if (/^\s*(eval code@|at eval \(|at <anonymous>:)/.test(lines[i])) break;
      }
      try {
        Object.defineProperty(e, "stack", { value: lines.slice(0, i).join("\n"), configurable: true });
      } catch (_) {}
    }
    return "Uncaught " + fmt(e);
  }

  window.__webframe_eval = function (id, src) {
    var r;
    try {
      r = (0, eval)(src);
    } catch (e) {
      send("evalerr", "", evalErr(e), id);
      return;
    }
    Promise.resolve(r).then(
      function (v) { send("eval", "", fmt(v, true), id); },
      function (e) { send("evalerr", "", evalErr(e), id); }
    );
  };
})();
