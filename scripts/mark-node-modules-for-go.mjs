// Hides node_modules from the Go toolchain.
//
// The lint tooling lives at the repository root, so npm installs into
// ./node_modules -- which sits inside the Go module. Go's `./...` walks it,
// and at least one dependency (flatted) ships a vendored .go file, so after
// any `npm install` the toolchain starts analysing code this repository does
// not own:
//
//   $ go list ./... | grep node_modules
//   github.com/alvarorichard/Goanime/node_modules/flatted/golang/pkg/flatted
//
// It is not cosmetic. Those 164 uncovered statements land in the denominator
// of `go test -cover ./...`, so the local coverage number drifts about half a
// point below the one CI computes -- CI never runs npm and Go in the same
// job, so it never sees them. Chasing that gap is a genuinely confusing way
// to lose an afternoon.
//
// Go stops descending at a module boundary, so an otherwise empty go.mod
// inside node_modules is enough to make the whole tree invisible. It cannot
// be committed, because `npm ci` deletes node_modules wholesale before
// installing; recreating it from postinstall is what makes it survive.
//
// The alternative was moving the tooling into a subdirectory with its own
// go.mod. That works too, but it pushes every path in eslint.config.mjs and
// check-frontend.mjs up a level and moves `npm run check` off the root for
// what is, from the frontend's point of view, no reason at all.

import { writeFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const nodeModules = join(repoRoot, "node_modules");

if (!existsSync(nodeModules)) {
  mkdirSync(nodeModules, { recursive: true });
}

const marker = join(nodeModules, "go.mod");
const contents = `// Not a real module. Written by scripts/mark-node-modules-for-go.mjs on
// every npm install so the Go toolchain stops descending into node_modules;
// see that file for why. Nothing imports this and nothing should.
module goanime-node-modules-placeholder

go 1.21
`;

writeFileSync(marker, contents);
console.log("node_modules/go.mod written: hidden from go ./...");
