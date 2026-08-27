import js from "@eslint/js";
import globals from "globals";

// The frontend is hand-written ES modules loaded straight from the embedded
// dist/ — no bundler, no transpiler, no import map. So the rules that matter
// most here are the ones a build step would otherwise catch for free: a name
// that no longer exists, an import that resolves to nothing, a variable left
// behind by a deleted feature.
export default [
  {
    ignores: ["node_modules/**", "cmd/goanime-gui/frontend/src/**"],
  },

  js.configs.recommended,

  {
    files: ["cmd/goanime-gui/frontend/dist/js/**/*.js"],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: "module",
      globals: {
        ...globals.browser,
        // Injected by the Wails runtime at load time, not importable.
        go: "readonly",
        runtime: "readonly",
      },
    },
    rules: {
      // The point of the whole setup. A feature removed by hand leaves
      // exactly this behind: a binding nobody reads any more.
      "no-unused-vars": [
        "error",
        { args: "after-used", argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],

      // `window.__debug` is assigned on purpose; console.warn/error carry
      // real diagnostics that ship. console.log does not.
      "no-console": ["warn", { allow: ["warn", "error"] }],

      // A caught error that is silently dropped has to say so. Several
      // catches here are deliberate no-ops (blocked localStorage, an
      // aborted fetch) and they all carry a comment explaining why.
      "no-empty": ["error", { allowEmptyCatch: true }],

      // These three are the ones that actually bite in a no-build setup:
      // a typo'd global reads as undefined at runtime with no error until
      // the line executes.
      "no-undef": "error",
      "no-implicit-globals": "error",
      "no-shadow-restricted-names": "error",

      eqeqeq: ["error", "smart"],
      "prefer-const": "error",
      "no-var": "error",
    },
  },

  {
    files: ["scripts/**/*.mjs"],
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: "module",
      globals: globals.node,
    },
  },
];
