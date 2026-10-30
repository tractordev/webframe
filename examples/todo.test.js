// Tests todo.html. Run with -x after injecting mock-fetch.js with -i, and
// with -y so the delete confirmation is accepted. Exits 0 if every check
// passes, 1 otherwise.
(async () => {
  const $ = (s) => document.querySelector(s);
  const items = () => [...document.querySelectorAll("#list li")].map((li) => li.firstChild.textContent.trim());
  const tick = () => new Promise((r) => setTimeout(r, 0));
  let failed = 0;
  const check = (name, got, want) => {
    const ok = JSON.stringify(got) === JSON.stringify(want);
    if (!ok) failed++;
    console.log(ok ? `ok   ${name}` : `FAIL ${name}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  };

  await tick();
  check("loads saved todos", items(), ["Buy milk"]);

  $("#text").value = "Walk dog";
  $("#add").requestSubmit();
  await tick();
  check("adds a todo", items(), ["Buy milk", "Walk dog"]);

  $("#text").value = "  ";
  $("#add").requestSubmit();
  await tick();
  check("ignores blank input", items(), ["Buy milk", "Walk dog"]);

  $("#list .delete").click();
  await tick();
  check("deletes after confirm", items(), ["Walk dog"]);

  check("saves each change", requests, ["GET /api/todos", "PUT /api/todos", "PUT /api/todos"]);

  webframe.exit(failed ? 1 : 0);
})();
