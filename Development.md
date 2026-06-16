你需要安装 golang、buf 并将其添加到 PATH。
你还需要pnpm，pnpm会帮你安装好 前端依赖项 以及本项目依赖的 nx。

pnpm nx show projects
pnpm nx run proto:generate
pnpm nx run web:lint
pnpm nx run web:test
pnpm nx run api:test
pnpm nx run api:vet
pnpm nx run api:build
pnpm nx run workspace:build
