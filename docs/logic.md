# Логика работы

Документ описывает поведение процесса от старта до алерта и ответа бота.

## Старт

```mermaid
sequenceDiagram
  participant Main
  participant MySQL
  participant Sched as Scheduler
  participant Bot as TelegramBot
  participant HTTP as HttpServer

  Main->>Main: config.Load из env
  loop до 30 попыток
    Main->>MySQL: ping
  end
  Main->>MySQL: миграции
  Main->>Sched: Run в горутине
  alt токен и chat id заданы
    Main->>Bot: RunBot в горутине
  end
  Main->>HTTP: ListenAndServe
  Note over Main: ждёт SIGINT или SIGTERM
```

Пока MySQL не готов, процесс не открывает UI. `/healthz` станет `200 ok` только после ping.

## Цикл проверки URL

Планировщик тикает каждую секунду и заново читает включённые мониторы из БД. Изменение интервала, URL или выключение в UI подхватывается без перезапуска.

```mermaid
flowchart TB
  Tick[Тик 1 секунда]
  Load[ListEnabledMonitors]
  Due{Прошло interval_seconds с lastRun?}
  Busy{Этот id уже в полёте?}
  Queue[Поставить в очередь воркеров]
  Skip[Пропустить этот тик]
  Worker[Воркер: GET]
  Save[INSERT checks]
  Alert[alerter.Handle]
  Tick --> Load --> Due
  Due -->|нет| Skip
  Due -->|да| Busy
  Busy -->|да| Skip
  Busy -->|нет| Queue --> Worker --> Save --> Alert
```

Правила очереди:

- один монитор не проверяется параллельно (`inFlight`);
- `lastRun` ставится в момент постановки в очередь, интервал считается между стартами, а не между окончаниями;
- если канал воркеров заполнен, слот снимается и монитор попробуют снова через секунду;
- число воркеров — `CHECKER_WORKERS` (по умолчанию 8);
- редиректы: не больше 5.

Раз в час (и сразу при старте) удаляются проверки старше `STATS_RETENTION_DAYS`.

## Как классифицируется ответ

Checker делает `GET` с заголовками `User-Agent: webChecker/1.0` и `Accept: */*`. Таймаут берётся из монитора. Время считается от `Do` до конца чтения тела (не больше 1 МиБ).

```mermaid
flowchart TB
  Get[HTTP GET]
  Err{Ошибка транспорта или таймаут?}
  Status{status равен expected_status?}
  SlowQ{response_ms больше или равен порогу?}
  Down[ok false]
  Up[ok true]
  SlowFlag[slow true]
  Fast[slow false]
  Get --> Err
  Err -->|да| Down
  Err -->|нет| Status
  Status -->|нет| Down
  Status -->|да| Up
  Down --> SlowQ
  Up --> SlowQ
  SlowQ -->|да| SlowFlag
  SlowQ -->|нет| Fast
```

Примеры:

- сайт отвечает `200` за 120 мс при пороге 3000 мс → `ok`, не `slow`;
- сайт отвечает `200` за 4000 мс при пороге 3000 мс → `ok` и `slow` (доступен, но медленный);
- сайт отвечает `500` при ожидаемом `200` → не `ok`;
- таймаут 10 с при пороге 3000 мс → не `ok`, `slow` (время ожидания уже больше порога), `status_code` пустой.

Дашборд для последней проверки показывает `down`, если не `ok`; иначе `slow`, если медленно; иначе `ok`.

## Алерты Telegram

Алерты — это не «каждое падение», а смена устойчивого состояния. Порог устойчивости — `fail_threshold` монитора (N подряд проблем).

Проблема для счётчика:

- `ok = false`, или
- `slow = true` **и** включён `TELEGRAM_SLOW_ALERTS`.

Если `TELEGRAM_SLOW_ALERTS=false`, медленный успешный ответ счётчик не увеличивает и сообщение «долгий ответ» не уходит.

```mermaid
stateDiagram-v2
  [*] --> Healthy: consecutive 0
  Healthy --> Counting: проблема
  Counting --> Counting: ещё проблема, счётчик меньше N
  Counting --> Alerted: счётчик достиг N
  Alerted --> Alerted: те же проблемы, повторных сообщений нет
  Alerted --> Healthy: ok и не slow
  Counting --> Healthy: ok и не slow
```

При переходе в Alerted:

1. если не `ok` и ещё не `down_alerted` → «URL недоступен»;
2. если slow-проблема и ещё не `slow_alerted` → «Долгий ответ»;
3. оба сообщения могут уйти на одной проверке (таймаут: и недоступен, и медленный).

При возврате в норму, если раньше был любой алерт → одно сообщение «URL восстановлен», флаги сбрасываются.

Тестовая кнопка в настройках вызывает `Send`, а не `Notify`. Она проверяет доставку в чат и **не требует** `TELEGRAM_ENABLED=true`. Непрошенные алерты требуют и конфигурацию, и `TELEGRAM_ENABLED`.

Команды бота принимаются только из `TELEGRAM_CHAT_ID`. Сообщения из других чатов отбрасываются.

## Команды бота

Long polling: `getUpdates` с таймаутом 25 с. Перед циклом вызывается `deleteWebhook`, чтобы polling не конфликтовал с webhook.

| Ввод | Ответ |
| --- | --- |
| `list`, `/list` | таблица `id \| имя \| url` |
| `stat`, `/stat` | `id \| имя \| статус` |
| `/stat 3`, `/stat3`, `stat_3`, `/stat id=3` | карточка статистики одного URL |
| `help`, `/start` | список команд |

Статистика по id читает монитор, последнюю проверку и агрегаты 24ч/7д. Если id нет — «Монитор N не найден».

## Web-интерфейс

Пользователь работает с теми же таблицами, что и планировщик.

```mermaid
flowchart LR
  Dash[Дашборд] --> Form[Создать или править]
  Dash --> Stats[Страница статистики]
  Dash --> Toggle[Вкл Выкл]
  Form --> Store[(MySQL monitors)]
  Stats --> Checks[(MySQL checks)]
  Settings[Настройки] --> Export[JSON файл]
  Settings --> Import[Замена БД]
  Settings --> TestTG[Тест Telegram]
```

Валидация формы на сервере: схема URL, диапазоны интервала, статуса, таймаута, порога мс и N. Некорректная форма рисуется снова с текстом ошибки, запись в БД не идёт.

График на странице монитора запрашивает `/monitors/{id}/checks.json` (до 200 последних точек) и рисует Chart.js; если библиотека не загрузилась, есть запасной canvas.

## Типичные сценарии

**Добавили URL.** На следующем тике `lastRun` ещё нет → проверка сразу. Через интервал — снова.

**Порог N = 3, сеть моргнула один раз.** Счётчик 1, затем успех сбрасывает его в 0. Telegram молчит.

**Три таймаута подряд.** После третьего — «недоступен» и при включённых slow-алертах «долгий ответ». Пока URL лежит, новых копий этих сообщений нет. Первый нормальный ответ — «восстановлен».

**Экспорт перед обновлением.** «Настройки → Скачать JSON». На новой среде — импорт. Планировщик начнёт проверять импортированные мониторы как обычно; история checks уже в файле.

## Наблюдаемость

Логи — `log/slog` в stdout контейнера.

Полезные сообщения:

- `telegram configured=... alerts=... slow_alerts=...` при старте;
- предупреждение, если токен есть, а `TELEGRAM_ENABLED=false`;
- `check problem` при `ok=false` или `slow=true`;
- `telegram alert kind=down|slow|recovery`;
- `telegram alert skipped` если алерты выключены;
- `telegram command ignored: chat id mismatch`.

`GET /healthz` без пароля удобен для Docker/балансировщика: `200` только при живой БД.
