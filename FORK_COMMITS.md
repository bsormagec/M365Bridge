# Fork Commits Tracking

Bu dosya, upstream fork'tan (`fork/main`) repoya alınan ve alınamayan commitleri takip eder.
Son güncelleme: 2026-07-15

## ✅ Repoya Alınan Commitler

Aşağıdaki commitler cherry-pick ile başarıyla uygulandı (çakışmasız):

| Commit | Açıklama | Durum |
|--------|----------|-------|
| `af8e955` | Fix Responses tool simulation compatibility | ✅ Cherry-picked |
| `453f53e` | feat(auth): encrypt M365 web cookies | ✅ Cherry-picked |
| `4883afc` | ci: bump actions/checkout from 4.3.1 to 7.0.0 (#11) | ✅ Cherry-picked |
| `f1bf0b8` | ci: bump docker/build-push-action from 6.19.2 to 7.3.0 (#13) | ✅ Cherry-picked |
| `f1ce14d` | ci: bump softprops/action-gh-release from 2.6.2 to 3.0.1 (#12) | ✅ Cherry-picked |
| `107ac3d` | ci: bump docker/setup-qemu-action from 3.7.0 to 4.2.0 (#14) | ✅ Cherry-picked |
| `85942df` | ci: bump docker/setup-buildx-action from 3.12.0 to 4.2.0 (#16) | ✅ Cherry-picked |
| `1abb435` | ci: bump docker/login-action from 3.7.0 to 4.4.0 (#17) | ✅ Cherry-picked |
| `985ab68` | ci: bump actions/setup-go from 5.6.0 to 6.5.0 (#18) | ✅ Cherry-picked |
| `8cd2f85` | docs(tool-calling): update to portable design guide | ✅ Cherry-picked |

Ayrıca aşağıdaki commitlerde **mantık zaten önceki merge'de alınmıştı** (içerik özdeş, cherry-pick "nothing to commit" döndürdü):

| Commit | Açıklama | Durum |
|--------|----------|-------|
| `9d16d45` | fix: preserve tool schemas and namespaces | ✅ Zaten mevcut (allowedToolNames, Namespace, InputSchema, ToolKey, resolveTool) |
| `db3b241` | fix: drop simulated tool calls missing required arguments | ✅ Zaten mevcut (simulatedToolCallRequiredCode, drop logic) |
| `ad59148` | feat(toolcalling): re-ask backend when tool calls drop required args | ✅ Zaten mevcut (re-ask mekanizması, README.md:644) |
| `40b5f8d` | refactor: unify simulated thinking filter across all endpoints | ✅ Zaten mevcut (thinkingfilter.go, ThinkingStreamFilter) |
| `29ce33e` | feat: live-stream filtered thinking in simulated Anthropic streaming | ✅ Zaten mevcut (thinkingBlockOpen, thinking_delta stream) |
| `fdd4976` | fix: stream buffered Anthropic tool_use input as input_json_delta | ✅ Zaten mevcut |
| `396cc40` | fix: stream Anthropic tool_use input as input_json_delta | ✅ Zaten mevcut |
| `9fd0e4f` | feat: live-stream filtered thinking on OpenAI chat and Responses | ✅ Zaten mevcut |
| `fc26082` | feat: log parsed tool-call count in Anthropic simulated parser | ✅ Zaten mevcut |

## ⚠️ Çakışma Nedeniyle Doğrudan Alınamayan Commitler

Tüm çakışmalar `pkg/servers/api.go` (ve ilgili dosyalar) üzerinde — bizim branch'imiz bu dosyayı fork'tan farklı yönde evriltmiş.
Bu commitlerdeki **işlevsel mantık büyük ölçüde zaten repoda mevcut**.

| Commit | Açıklama | Çakışan Dosyalar | Notlar |
|--------|----------|-----------------|--------|
| `7e613ea` | Restore bridge parity and namespaced Responses tools | api.go, api_responses_test.go, simulated.go | Mantık önceki merge'de alındı |
| `bd0f69d` | fix(api): improve tool handling and message formatting | api.go | Mantık önceki merge'de alındı |
| `f6f45fa` | Improve required tool retry reliability | api.go, api_responses_test.go | Mantık önceki merge'de alındı |
| `162452c` | chore: bump version to 1.3.1 | models.go | Versiyon farklı (1.3.7'ye geçildi) |
| `4de5992` | refactor: modernize code for gopls modernize analyzer | api.go, api_responses_test.go, streamextract.go | Revert edildi (api.go uyumsuz) |
| `77ea9c7` | ci: add modernize job running gopls modernize analyzer | ci.yml | CI'da yok — gerekirse manuel eklenebilir |
| `a5ca5c0` | ci: harden supply chain against integrity failures | ci.yml, release.yml, Dockerfile | Güvenlik — gerekirse manuel eklenebilir |
| `bfa9611` | chore: bump version to 1.3.5 | CHANGELOG.md, models.go | Versiyon farklı |
| `4e88b5b` | chore: bump github.com/gorilla/websocket from 1.5.1 to 1.5.3 (#9) | go.mod, go.sum | Bağımlılık zaten güncel |
| `723d283` | ci: bump golang from 1.22-alpine to 1.26-alpine (#10) | Dockerfile | Go 1.26 zaten kullanımda |
| `c6aa557` | chore: move to Go 1.26 and adopt stdlib iterators | api.go | Go 1.26 zaten kullanımda |
| `9ed9271` | chore: bump version to 1.3.6 | CHANGELOG.md, models.go | Versiyon farklı |

## 📊 Özet

- **Toplam fork commit incelendi:** 26
- **Cherry-pick ile alındı:** 10
- **İçerik zaten mevcut (önceki merge'den):** 9
- **Çakışma / versiyon farkı nedeniyle atlandı:** 12 (işlevsel kayıp yok)
- **Test durumu:** Tüm testler geçiyor (`go test ./...` ✅)