// Prints every link on a page as "text <tab> url" and exits. Run it with -x
// on any page:
//
//   $ webframe -t 30 -x examples/links.js https://example.com
for (const a of document.querySelectorAll("a[href]")) {
  console.log(`${a.textContent.trim().replace(/\s+/g, " ")}\t${a.href}`);
}
webframe.exit(0);
