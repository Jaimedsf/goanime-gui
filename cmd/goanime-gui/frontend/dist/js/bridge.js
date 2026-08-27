export function app() {
  if (!window.go || !window.go.main || !window.go.main.App) {
    throw new Error(
      "Ponte com o Wails indisponível — rode o executável ou `wails dev`."
    );
  }
  return window.go.main.App;
}

// errText unwraps the many shapes a rejected Wails call can take so the
// user sees the Go error text rather than "[object Object]".
export function errText(err) {
  if (!err) return "erro desconhecido";
  if (typeof err === "string") return err;
  return err.message || String(err);
}

// withTimeout gives up on a bridge call that never settles. Without it such a
// call leaves a skeleton on screen with no error and no way out — which is
// exactly what "carregamento infinito" looks like from the outside.
export function withTimeout(promise, ms, what) {
  let timer;
  return Promise.race([
    promise.finally(() => clearTimeout(timer)),
    new Promise((_, reject) => {
      timer = setTimeout(
        () =>
          reject(
            new Error(`${what} não respondeu em ${Math.round(ms / 1000)}s`)
          ),
        ms
      );
    }),
  ]);
}

// runBounded applies fn over items with at most `limit` in flight. Failures
// are logged and skipped: a missing cover or date is cosmetic and must
// never break the grid.
export async function runBounded(items, limit, fn) {
  if (!items.length) return;
  let idx = 0;

  async function worker() {
    while (idx < items.length) {
      const item = items[idx++];
      try {
        await fn(item);
      } catch (err) {
        console.warn("card enrichment failed", err);
      }
    }
  }

  await Promise.all(
    Array.from({ length: Math.min(limit, items.length) }, worker)
  );
}
