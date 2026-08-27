// Structural checks ESLint cannot make, because they cross the JS/HTML line.
//
// The frontend reaches the DOM through one indirection: dom.js resolves every
// id once into an `els` object, and every other module says `els.catalogGrid`.
// That is good for readability and terrible for refactors — deleting a
// <section> from index.html leaves `$("adult-grid")` returning null, `els.x`
// undefined, and nothing fails until a user clicks the thing. There is no
// build step to catch it and no test that opens the page.
//
// So this walks the three links in that chain:
//
//   index.html  --id-->  dom.js  --els.X-->  every other module
//
// It also flags exports nobody imports, which is what a removed feature
// leaves behind, and imports naming a symbol the target module does not
// export, which is what a half-finished rename leaves behind.
//
// Run with `npm run check:frontend`. Exits non-zero on any FAIL.

import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const frontend = join(repoRoot, "cmd", "goanime-gui", "frontend", "dist");
const jsDir = join(frontend, "js");

const modules = readdirSync(jsDir)
  .filter((f) => f.endsWith(".js"))
  .sort();
const source = Object.fromEntries(
  modules.map((f) => [f, readFileSync(join(jsDir, f), "utf8")]),
);
const html = readFileSync(join(frontend, "index.html"), "utf8");

const problems = [];
const fail = (file, msg) => problems.push(`${file}: ${msg}`);

// --- what dom.js declares -------------------------------------------------
// Entries look like `  catalogGrid: $("catalog-grid"),` — two-space indented
// keys of the single exported object literal.
const domSource = source["dom.js"];
if (!domSource) {
  console.error("check-frontend: dom.js not found in " + jsDir);
  process.exit(1);
}
const declared = new Set(
  [...domSource.matchAll(/^ {2}([A-Za-z_$][\w$]*):/gm)].map((m) => m[1]),
);

// --- 1. every els.X used anywhere is declared -----------------------------
for (const [file, body] of Object.entries(source)) {
  if (file === "dom.js") continue;
  for (const m of body.matchAll(/\bels\.([A-Za-z_$][\w$]*)/g)) {
    if (!declared.has(m[1])) {
      fail(file, `els.${m[1]} is used but dom.js does not declare it`);
    }
  }
}

// --- 2. every $("id") in dom.js exists in index.html ----------------------
const htmlIds = new Set(
  [...html.matchAll(/\bid="([^"]+)"/g)].map((m) => m[1]),
);
for (const m of domSource.matchAll(/\$\("([^"]+)"\)/g)) {
  if (!htmlIds.has(m[1])) {
    fail("dom.js", `$("${m[1]}") has no matching id in index.html`);
  }
}

// --- 3. imports resolve to real exports -----------------------------------
const exportsOf = (body) => {
  const names = new Set();
  const decl = /^export\s+(?:async\s+)?(?:function|const|let|class)\s+([A-Za-z_$][\w$]*)/gm;
  for (const m of body.matchAll(decl)) names.add(m[1]);
  // `export { a, b as c }`
  for (const m of body.matchAll(/^export\s*\{([^}]*)\}/gm)) {
    for (const part of m[1].split(",")) {
      const alias = part.trim().split(/\s+as\s+/);
      const name = (alias[1] ?? alias[0])?.trim();
      if (name) names.add(name);
    }
  }
  return names;
};
const exported = Object.fromEntries(
  Object.entries(source).map(([f, b]) => [f, exportsOf(b)]),
);

const importSites = [];
for (const [file, body] of Object.entries(source)) {
  const re = /^import\s*\{([^}]+)\}\s*from\s*"\.\/([^"]+)"/gm;
  for (const m of body.matchAll(re)) {
    const target = m[2];
    if (!exported[target]) {
      fail(file, `imports from "./${target}", which does not exist`);
      continue;
    }
    for (const part of m[1].split(",")) {
      const name = part.trim().split(/\s+as\s+/)[0];
      if (!name) continue;
      importSites.push({ file, target, name });
      if (!exported[target].has(name)) {
        fail(file, `imports ${name} from ${target}, which does not export it`);
      }
    }
  }
}

// --- 4. exports nobody imports --------------------------------------------
// A warning, not a failure: dom.js exports `$` for local use and a module may
// legitimately export something only index.html or the debug hook reaches.
const importedNames = new Set(importSites.map((s) => `${s.target}#${s.name}`));
const orphans = [];
for (const [file, names] of Object.entries(exported)) {
  for (const name of names) {
    if (!importedNames.has(`${file}#${name}`)) orphans.push(`${file}: ${name}`);
  }
}

// --- report ---------------------------------------------------------------
console.log(`checked ${modules.length} modules, ${declared.size} DOM bindings`);

if (orphans.length) {
  console.log("\nexported but never imported (dead code, or reached another way):");
  for (const o of orphans) console.log(`  ${o}`);
}

if (problems.length) {
  console.log(`\n${problems.length} problem(s):`);
  for (const p of problems) console.log(`  FAIL ${p}`);
  process.exit(1);
}

console.log("\nall structural checks passed");
