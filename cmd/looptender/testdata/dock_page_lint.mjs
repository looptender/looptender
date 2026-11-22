// Fails when dock.js calls one of its own helper functions through a local
// variable or parameter of the same name: `var tag = ...` inside a callback
// made every later tag(...) call in it a call of an element ("tag is not a
// function"). The binding of each call is resolved with the TypeScript
// compiler's own scope analysis.
//
//   node dock_page_lint.mjs <dock.js>      (TS_DIR names the typescript package)
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const ts = require(process.env.TS_DIR);
const file = process.argv[2];
const program = ts.createProgram([file], { allowJs: true, checkJs: false, noLib: true, noResolve: true, target: ts.ScriptTarget.ES2020 });
const checker = program.getTypeChecker();
const sf = program.getSourceFile(file);
const functions = new Set();
(function collect(n) {
  if (ts.isFunctionDeclaration(n) && n.name) functions.add(n.name.text);
  ts.forEachChild(n, collect);
})(sf);
const problems = [];
(function walk(n) {
  if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && functions.has(n.expression.text)) {
    const sym = checker.getSymbolAtLocation(n.expression);
    const decl = sym && sym.declarations && sym.declarations[0];
    if (decl && !ts.isFunctionDeclaration(decl)) {
      const at = sf.getLineAndCharacterOfPosition(n.getStart());
      problems.push(`dock.js:${at.line + 1}: ${n.expression.text}(...) calls a local ${ts.isParameter(decl) ? "parameter" : "variable"} named like the helper function ${n.expression.text}`);
    }
  }
  ts.forEachChild(n, walk);
})(sf);
if (functions.size < 50) { console.error(`found only ${functions.size} functions in dock.js: the lint did not read it`); process.exit(1); }
if (problems.length) { console.error(problems.join("\n")); process.exit(1); }
console.log(`${functions.size} helper functions: no call goes through a shadowing variable`);
