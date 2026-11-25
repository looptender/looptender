// Styles shared by the setup, attach and move panels. Colours come from the
// surrounding text (currentColor) and the system accent, so the panels follow
// BB's light and dark themes without naming either. Not a design system: just
// enough that a form reads as a form (grouped fields, visible selection,
// one prominent action, focus rings).
const css = `
.kgs{display:grid;gap:12px;padding:12px 14px;max-width:540px;min-width:240px;max-height:min(76vh,620px);overflow-y:auto;font-size:13px;line-height:1.4;color:inherit;box-sizing:border-box}
.kgs [role=tabpanel]{display:grid;gap:12px}
.kgs *,.kgs *::before,.kgs *::after{box-sizing:border-box}
.kgs h3,.kgs h4,.kgs p{margin:0}
.kgs h3[tabindex]:focus{outline:none}
.kgs h3{font-size:15px;font-weight:600}
.kgs h4{font-size:13px;font-weight:600}
.kgs .kgs-head{display:flex;align-items:flex-start;justify-content:space-between;gap:10px}
.kgs .kgs-min{font:inherit;font-size:12px;line-height:18px;padding:3px 9px;border:1px solid color-mix(in srgb,currentColor 32%,transparent);border-radius:6px;background:transparent;color:inherit;cursor:pointer;flex:none;opacity:.82}
.kgs .kgs-min:hover{opacity:1;background:color-mix(in srgb,currentColor 9%,transparent)}
.kgs .kgs-min:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
.kgs .kgs-intent-help{display:grid;gap:3px}
.kgs .lead,.kgs .help{opacity:.74}
.kgs .help{font-size:12px}
.kgs fieldset{border:0;margin:0;padding:0;min-width:0;display:grid;gap:6px}
.kgs legend{padding:0;margin:0 0 2px;font-weight:600}
.kgs .field{display:grid;gap:4px}
.kgs .field>label,.kgs .label{font-weight:600}
.kgs input[type=text],.kgs textarea,.kgs select{font:inherit;color:inherit;background:transparent;width:100%;padding:6px 8px;border:1px solid color-mix(in srgb,currentColor 38%,transparent);border-radius:6px}
.kgs textarea{resize:vertical;min-height:68px}
.kgs [aria-invalid=true]{border-color:#d2402b}
.kgs .err{color:color-mix(in srgb,#d2402b 55%,currentColor);font-size:12px}
.kgs .choice{display:grid;grid-template-columns:auto 1fr;column-gap:8px;row-gap:1px;align-items:start;padding:8px 10px;border:1px solid color-mix(in srgb,currentColor 24%,transparent);border-radius:8px;cursor:pointer}
.kgs .choice>input{margin:1px 0 0}
.kgs input[type=radio],.kgs input[type=checkbox]{width:18px;height:18px;flex:none;margin:0;accent-color:AccentColor}
.kgs .choice .t{font-weight:600}
.kgs .choice .help{grid-column:2}
.kgs .choice:hover{border-color:color-mix(in srgb,currentColor 50%,transparent)}
.kgs .choice[data-checked=true]{border-color:currentColor;background:color-mix(in srgb,currentColor 8%,transparent);box-shadow:inset 0 0 0 1px currentColor}
.kgs .choice[data-disabled=true]{opacity:.6;cursor:default}
.kgs .choice:focus-within{outline:2px solid #1a73e8;outline-offset:2px}
.kgs .actions{display:grid;gap:6px;justify-items:start}
.kgs .actions .row{display:flex;flex-wrap:wrap;gap:8px;align-items:center}
.kgs .btn{font:inherit;padding:6px 12px;border-radius:6px;border:1px solid color-mix(in srgb,currentColor 42%,transparent);background:transparent;color:inherit;cursor:pointer}
.kgs .btn:hover:not(:disabled){background:color-mix(in srgb,currentColor 9%,transparent)}
.kgs .btn.primary{font-weight:600;padding:7px 14px;border-color:transparent;background:Highlight;background:AccentColor;color:HighlightText;color:AccentColorText}
.kgs .btn.primary:hover:not(:disabled){filter:brightness(1.08);background:AccentColor}
.kgs .btn:disabled{opacity:.5;cursor:not-allowed}
.kgs .btn:focus-visible,.kgs summary:focus-visible,.kgs input:focus-visible,.kgs textarea:focus-visible,.kgs select:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
.kgs .card{display:grid;gap:8px;padding:10px 12px;border:1px solid color-mix(in srgb,currentColor 26%,transparent);border-radius:8px}
.kgs .card.done{border-color:color-mix(in srgb,#2e9d5b 70%,currentColor)}
.kgs dl{display:grid;grid-template-columns:max-content 1fr;gap:3px 10px;margin:0}
.kgs dt{opacity:.74}
.kgs dd{margin:0;overflow-wrap:anywhere}
.kgs pre{margin:0;white-space:pre-wrap;overflow-wrap:anywhere;padding:6px 8px;border-radius:6px;background:color-mix(in srgb,currentColor 7%,transparent);font-size:12px}
.kgs ul{margin:0;padding-left:18px}
.kgs .msg{padding:7px 10px;border-radius:6px;border:1px solid color-mix(in srgb,currentColor 24%,transparent)}
.kgs .msg[role=status]{border:0;padding:0;opacity:.8}
.kgs .msg[role=alert]{border-color:#d2402b;background:color-mix(in srgb,#d2402b 10%,transparent)}
.kgs .warn{padding:7px 10px;border-radius:6px;border:1px solid color-mix(in srgb,#b45309 48%,currentColor);background:color-mix(in srgb,#b45309 8%,transparent);font-size:12px;display:grid;gap:4px}
.kgs .tabs{display:grid;grid-template-columns:1fr 1fr;gap:4px;padding:3px;border:1px solid color-mix(in srgb,currentColor 22%,transparent);border-radius:8px;background:color-mix(in srgb,currentColor 4%,transparent)}
.kgs .tab{font:inherit;font-weight:500;color:inherit;background:transparent;border:0;border-radius:6px;padding:5px 8px;cursor:pointer;text-align:center}
.kgs .tab:hover:not(:disabled){background:color-mix(in srgb,currentColor 7%,transparent)}
.kgs .tab[aria-selected=true]{background:color-mix(in srgb,currentColor 13%,transparent);font-weight:600;box-shadow:0 1px 2px color-mix(in srgb,currentColor 10%,transparent)}
.kgs .tab:disabled{opacity:.55;cursor:not-allowed}
.kgs .tab:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
.kgs details{border:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:8px;padding:6px 10px}
.kgs details[open]{display:grid;gap:8px;padding:8px 10px 10px}
.kgs details fieldset>legend{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap}
.kgs details>summary,.kgs-more>summary{cursor:pointer;font-weight:600;list-style:none}
.kgs details>summary{font-size:12px}
.kgs summary::-webkit-details-marker,.kgs-more>summary::-webkit-details-marker{display:none}
.kgs summary::before,.kgs-more>summary::before{content:"\\25B8";display:inline-block;width:1.2em;opacity:.7}
.kgs details[open]>summary::before,.kgs-more[open]>summary::before{content:"\\25BE"}
.kgs-chip{font:inherit;font-size:12px;line-height:1.2;min-height:28px;padding:4px 10px;border:1px solid currentColor;border-radius:6px;background:none;color:inherit;cursor:pointer;white-space:nowrap}
.kgs-chip:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
@media (max-width:640px),(pointer:coarse){
.kgs{max-height:min(82vh,680px);max-height:min(82dvh,680px)}
.kgs input[type=text],.kgs input[type=number],.kgs textarea,.kgs select{font-size:16px;min-height:44px}
.kgs textarea{min-height:88px}
.kgs .btn{min-height:44px}
.kgs .kgs-min{min-height:44px;min-width:44px;padding:0 12px;font-size:14px}
.kgs .tab{min-height:44px;font-size:14px}
.kgs .choice{min-height:44px;padding:10px}
.kgs summary,.kgs-more>summary{min-height:44px;display:flex;align-items:center}
.kgs-chip{min-height:44px !important;padding:8px 14px !important;font-size:14px !important;line-height:20px !important;box-sizing:border-box}
}
.kg-banner-body{max-height:min(65vh,540px);overflow-y:auto;overscroll-behavior:contain}
.kg-banner-body .kgs{max-height:none;overflow-y:visible}
@media (max-width:640px),(pointer:coarse){.kg-banner-body{max-height:min(72vh,600px);max-height:min(72dvh,600px)}}
.kgs-more{display:grid;gap:10px;padding:0 14px 12px;max-width:540px;min-width:240px;font-size:13px;line-height:1.4;box-sizing:border-box}
.kgs-more>summary{padding:6px 0}
.kgs-more>summary:focus-visible{outline:2px solid #1a73e8;outline-offset:2px}
.kgs-more .kgs{padding:10px 0 4px;border-top:1px solid color-mix(in srgb,currentColor 18%,transparent);border-radius:0;max-height:none;overflow-y:visible}
`;
export function SetupStyles() {
  return <style>{css}</style>;
}
