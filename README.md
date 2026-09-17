# webChecker

Краткий запуск описан ниже. Устройство системы, схема БД и алгоритмы — в каталоге [docs](docs/README.md).

## Возможности

- GET-проверки URL с заданным интервалом, ожидаемым HTTP-статусом, таймаутом и порогом долгого ответа
- При старте проверки разнесены по времени (`id % interval` и последняя запись в БД), чтобы не бить все URL сразу
- Дашборд, CRUD мониторов, статистика за 24 часа и 7 дней, график времени ответа
- HTTP Basic Auth (логин и пароль из переменных окружения)
- Telegram: одно сообщение после N подряд проблем и отдельное — когда URL снова в норме
- Команды бота в чате: `list`, `stat`, `stat <id>`
- Экспорт и импорт данных БД из вкладки «Настройки»
- Хранение истории проверок в MySQL, очистка записей старше `STATS_RETENTION_DAYS`

## Запуск в Docker

```bash
cp .env.example .env
# при необходимости отредактируйте .env
docker compose up --build
```

Интерфейс: [http://localhost:8080](http://localhost:8080)

По умолчанию:

- логин `admin`
- пароль `changeme`

`GET /healthz` доступен без авторизации и возвращает `ok`, когда MySQL отвечает.

## Переменные окружения

| Переменная | Описание | По умолчанию |
| --- | --- | --- |
| `HTTP_ADDR` | Адрес HTTP-сервера | `:8080` |
| `BASIC_AUTH_USER` | Логин web UI | обязательно |
| `BASIC_AUTH_PASSWORD` | Пароль web UI | обязательно |
| `MYSQL_HOST` | Хост MySQL | `mysql` |
| `MYSQL_PORT` | Порт MySQL | `3306` |
| `MYSQL_USER` | Пользователь MySQL | обязательно |
| `MYSQL_DATABASE` | Имя БД | обязательно |
| `MYSQL_PASSWORD` | Пароль MySQL | пусто |
| `TELEGRAM_ENABLED` | Включить уведомления (`true`/`false`). Если токен и chat id заданы, а переменная пустая — уведомления включаются | `true`, если заданы токен и chat id |
| `TELEGRAM_BOT_TOKEN` | Токен бота | пусто |
| `TELEGRAM_CHAT_ID` | Chat id (личный чат или группа) | пусто |
| `TELEGRAM_SLOW_ALERTS` | Слать ли сообщения о долгом ответе (`true`/`false`) | `true` |
| `STATS_RETENTION_DAYS` | Сколько дней хранить проверки | `30` |
| `CHECKER_WORKERS` | Число параллельных проверок | `8` |
| `TZ` | Часовой пояс отображения времени | `Europe/Moscow` |

Если `TELEGRAM_ENABLED=true`, токен и chat id обязательны.

## Telegram

1. Создайте бота у [@BotFather](https://t.me/BotFather) и скопируйте токен.
2. Напишите боту любое сообщение (или добавьте его в группу).
3. Откройте `https://api.telegram.org/bot<TOKEN>/getUpdates` и возьмите `chat.id`.
4. Пропишите `TELEGRAM_ENABLED=true`, `TELEGRAM_BOT_TOKEN` и `TELEGRAM_CHAT_ID` в `.env`.
5. Перезапустите `docker compose up -d` и на странице «Настройки» отправьте тестовое сообщение.

Команды в том же чате (`TELEGRAM_CHAT_ID`), с `/` или без:

- `list` — список URL: id, название, url
- `stat` — id, название и текущий статус
- `stat 3` — статистика монитора с id `3`
- `help` — краткая справка

Алерты:

- после N подряд неуспешных проверок — сообщение о недоступности;
- после N подряд медленных ответов — сообщение о долгом ответе, если `TELEGRAM_SLOW_ALERTS=true`;
- когда статус снова совпадает с ожидаемым и время ответа в норме — сообщение о восстановлении;
- повторные сообщения на каждый тик не отправляются, пока состояние не изменится.

## Экспорт и импорт

На странице «Настройки» можно скачать JSON со всеми мониторами, проверками и состояниями алертов. Импорт полностью заменяет текущие данные этим файлом.

## Локальная сборка без Docker

Нужны Go 1.26 и доступный MySQL.

```bash
export BASIC_AUTH_USER=admin BASIC_AUTH_PASSWORD=changeme
export MYSQL_HOST=127.0.0.1 MYSQL_USER=webchecker MYSQL_PASSWORD=webchecker MYSQL_DATABASE=webchecker
go run ./cmd/webchecker
```

## Структура

```
cmd/webchecker/     точка входа
internal/           конфиг, БД, проверки, планировщик, Telegram, HTTP
web/templates/      HTML
web/static/css/     CSS
web/static/js/      JS (включая Chart.js)
migrations/         SQL-миграции
```
