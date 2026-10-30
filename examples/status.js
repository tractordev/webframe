// Checks that a page loads and reports problems: exits 1 if the page throws
// or a resource fails to load (with -e), or if its title doesn't contain
// webframe.env.EXPECT, and 0 otherwise. Useful as a smoke test after a deploy:
//
//   $ webframe -e -p -t 30 -E EXPECT=Example -x examples/status.js https://example.com
const want = webframe.env.EXPECT || "";
if (!document.title.includes(want)) {
  console.error(`title ${JSON.stringify(document.title)} doesn't contain ${JSON.stringify(want)}`);
  webframe.exit(1);
}
console.log(`ok ${document.title} (${performance.getEntriesByType("resource").length} resources)`);
webframe.exit(0);
