// Styles shared by the setup, attach and move panels. Colours come from the
// surrounding text (currentColor) and the system accent, so the panels follow
// BB's light and dark themes without naming either. Not a design system: just
// enough that a form reads as a form (grouped fields, visible selection,
// one prominent action, focus rings).
const css = `
.kgs{display:grid;gap:18px;padding:16px;max-width:560px;min-width:240px;font-size:13px;line-height:1.45;color:inherit;box-sizing:border-box}
.kgs *,.kgs *::before,.kgs *::after{box-sizing:border-box}
.kgs h3,.kgs h4,.kgs p{margin:0}
.kgs h3[tabindex]:focus{outline:none}
.kgs h3{font-size:15px;font-weight:600}
.kgs h4{font-size:13px;font-weight:600}
.kgs .lead,.kgs .help{opacity:.74}
.kgs .help{font-size:12px}
.kgs fieldset{border:0;margin:0;padding:0;min-width:0;display:grid;gap:8px}
.kgs legend{padding:0;margin:0 0 2px;font-weight:600}
.kgs .field{display:grid;gap:4px}
.kgs .field>label,.kgs .label{font-weight:600}
.kgs input[type=text],.kgs textarea,.kgs select{font:inherit;color:inherit;background:transparent;width:100%;padding:7px 9px;border:1px solid color-mix(in srgb,currentColor 38%,transparent);border-radius:6px}
.kgs textarea{resize:vertical;min-height:96px}
.kgs [aria-invalid=true]{border-color:#d2402b}
.kgs .err{color:color-mix(in srgb,#d2402b 55%,currentColor);font-size:12px}
.kgs .choice{display:grid;grid-template-columns:auto 1fr;column-gap:10px;row-gap:2px;align-items:start;padding:10px 12px;border:1px solid color-mix(in srgb,currentColor 24%,transparent);border-radius:8px;cursor:pointer}
.kgs .choice>input{margin:1px 0 0}
.kgs input[type=radio],.kgs input[type=checkbox]{width:20px;height:20px;flex:none;margin:0;accent-color:AccentColor}
.kgs .choice .t{font-weight:600}
.kgs .choice .help{grid-column:2}
.kgs .choice:hover{border-color:color-mix(in srgb,currentColor 50%,transparent)}
.kgs .choice[data-checked=true]{border-color:currentColor;background:color-mix(in srgb,currentColor 8%,transparent);box-shadow:inset 0 0 0 1px currentColor}
.kgs .choice[data-disabled=true]{opacity:.6;cursor:default}
.kgs .choice:focus-within{outline:2px solid #1a73e8;outline-offset:2px}
.kgs .actions{display:grid;gap:6px;justify-items:start}
.kgs .actions .row{display:flex;flex-wrap:wrap;gap:8px;align-items:center}
.kgs .btn{font:inherit;padding:8px 14px;border-radius:6px;border:1px solid color-mix(in srgb,currentColor 42%,transparent);background:transparent;color:inherit;cursor:pointer}
.kgs .btn:hover:not(:disabled){background:color-mix(in srgb,currentColor 9%,transparent)}
.kgs .btn.primary{font-weight:600;padding:9px 18px;border-color:transparent;background:Highlight;background:AccentColor;color:HighlightText;color:AccentColorText}
.kgs .btn.primary:hover:not(:disabled){filter:brightness(1.08);background:AccentColor}
.kgs .btn:disabled{opacity:.5;cursor:not-allowed}
.kgs .btn:focus-visible,.kgs summary:focus-visible,.kgs input:focus-visible,.kgs textarea:focus-visible,.kgs select:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
.kgs .card{display:grid;gap:10px;padding:12px 14px;border:1px solid color-mix(in srgb,currentColor 26%,transparent);border-radius:8px}
.kgs .card.done{border-color:color-mix(in srgb,#2e9d5b 70%,currentColor)}
.kgs dl{display:grid;grid-template-columns:max-content 1fr;gap:4px 12px;margin:0}
.kgs dt{opacity:.74}
.kgs dd{margin:0;overflow-wrap:anywhere}
.kgs pre{margin:0;white-space:pre-wrap;overflow-wrap:anywhere;padding:8px 10px;border-radius:6px;background:color-mix(in srgb,currentColor 7%,transparent);font-size:12px}
.kgs ul{margin:0;padding-left:18px}
.kgs .msg{padding:8px 10px;border-radius:6px;border:1px solid color-mix(in srgb,currentColor 24%,transparent)}
.kgs .msg[role=status]{border:0;padding:0;opacity:.8}
.kgs .msg[role=alert]{border-color:#d2402b;background:color-mix(in srgb,#d2402b 10%,transparent)}
.kgs details>summary,.kgs-more>summary{cursor:pointer;font-weight:600;list-style:none}
.kgs summary::-webkit-details-marker,.kgs-more>summary::-webkit-details-marker{display:none}
.kgs summary::before,.kgs-more>summary::before{content:"\\25B8";display:inline-block;width:1.2em;opacity:.7}
.kgs details[open]>summary::before,.kgs-more[open]>summary::before{content:"\\25BE"}
.kgs-chip{font:inherit;font-size:12px;line-height:1.2;min-height:28px;padding:4px 10px;border:1px solid currentColor;border-radius:6px;background:none;color:inherit;cursor:pointer;white-space:nowrap}
.kgs-chip:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
@media (max-width:640px),(pointer:coarse){
.kgs input[type=text],.kgs input[type=number],.kgs textarea,.kgs select{font-size:16px;min-height:44px}
.kgs textarea{min-height:120px}
.kgs .btn{min-height:44px}
.kgs .choice{min-height:44px;padding:12px}
.kgs summary,.kgs-more>summary{min-height:44px;display:flex;align-items:center}
.kgs-chip{min-height:44px !important;padding:8px 14px !important;font-size:14px !important;line-height:20px !important;box-sizing:border-box}
}
.kgs-more{display:grid;gap:12px;padding:0 16px 16px;max-width:560px;min-width:240px;font-size:13px;line-height:1.45;box-sizing:border-box}
.kgs-more>summary{padding:8px 0}
.kgs-more>summary:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
.kgs-more .kgs{padding:12px 0 4px;border-top:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:0}
`;
export function SetupStyles() {
  return <style>{css}</style>;
}
