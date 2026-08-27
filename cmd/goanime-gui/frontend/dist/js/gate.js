import { app, errText } from "./bridge.js";
import { state } from "./state.js";
import { els, toast } from "./dom.js";
import { refreshGateNote } from "./episodes.js";

export async function openGateSettings() {
  try {
    const opts = await app().GetGateOptions();
    els.gateBundled.checked = !!opts.bundled;
    els.gateHeadless.checked = !!opts.headless;
    els.gateChannel.value = opts.channel || "";
  } catch (err) {
    console.warn("GetGateOptions failed", err);
  }
  els.gateModal.hidden = false;
  els.gateBundled.focus();
}

export async function saveGateSettings() {
  try {
    await app().SetGateOptions({
      bundled: els.gateBundled.checked,
      headless: els.gateHeadless.checked,
      channel: els.gateChannel.value,
    });
    toast("Configuração salva — vale a partir da próxima reprodução");
  } catch (err) {
    toast(`Não foi possível salvar: ${errText(err)}`);
  }
  els.gateModal.hidden = true;
  if (state.result) refreshGateNote(state.result);
}
