// Run with -x on smoke.html. Exits 0 if confirm() was accepted, else 3.
(async () => {
  console.log("init", window.INITED || "no");
  console.log("loaded", document.readyState === "complete" ? "yes" : "no");
  const ok = confirm("sure?");
  console.log("confirm", ok);
  webframe.exit(ok ? 0 : 3);
})();
