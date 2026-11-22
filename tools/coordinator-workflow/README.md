# Offline coordinator workflow checks

The usable packaged entry point, grant schema, commands and limitations are in
[the skill reference](../../integrations/bb-plugin-keepgoing/skills/keepgoing/references/coordinator-workflow.md).
See [the reviewed design contract](DESIGN.md) for persistence and submission bounds.

Run the same production consumer/adapter against fictional commands:

```sh
KEEPGOING_TEST_BINARY=<absolute-built-cli> python3 -B -m unittest discover -s tools/coordinator-workflow -p 'test_*.py' -v
```

The explicitly supplied CLI adds a real assignment-writer/control-preservation
journey, still using a fake BB and isolated runtime/registry. Without it that case
skips explicitly. No installed BB/provider, live loop or catalog is used.

A deterministic dispatcher pass is not actual agent continuation, installed
acceptance or a global authority/spend guarantee. Automatic delegation acceptance
remains incomplete; the agent consumes this workflow under an existing owner
grant, and the supervisor never invokes it.
