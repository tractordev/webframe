// Run with -x before script.js to check -x files run in order.
new Promise(r => setTimeout(() => { console.log("first ran"); r(); }, 50));
