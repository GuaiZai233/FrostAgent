# General Guidelines

- Build orchestration uses the root `Makefile`.
- Prefer `make` targets for workspace tasks such as `make build`, `make dev`, `make lint`, `make vet`, and `make proto-generate`.
- Package scripts in `package.json` delegate to Makefile targets; use `pnpm <script>` only when matching existing developer habits is useful.
- Angular commands should go through the local CLI, for example `pnpm exec ng build web`.
- Protobuf generation uses `buf generate` with the checked-in `buf.gen*.yaml` templates.

写完代码后不需要测试，最多运行一下语法检查。人类自己会测试你的代码的！
