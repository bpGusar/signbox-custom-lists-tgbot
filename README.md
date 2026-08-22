# lst-signbox-lists-tgbot

Лёгкий Telegram-бот для OpenWrt 24.10: управление файлами `domain_list.lst` и `ip_list.lst` через inline-кнопки.

## Возможности

- Приём списка доменов или IP/CIDR (через запятую, пробел или с новой строки)
- Автоопределение типа (домен / IP)
- Действия: добавить, удалить, отключить (`// value`), отмена
- Умная обработка уже существующих и отключённых записей
- Проверка файлов при `/start`, создание при отсутствии
- Предупреждение о несинхронизированном перезапуске сервиса + кнопка перезапуска
- Уведомление в чат о выходе новой версии
- Обновление бота кнопкой прямо из чата («Настройки» → «Обновить бота»)
- Настройка через LuCI: токен бота и пути к файлам



## Быстрый старт



### 1. Создайте бота в Telegram

1. Откройте [@BotFather](https://t.me/BotFather)
2. `/newbot` → задайте имя и username
3. Скопируйте токен

> Бот привязывается к первому написавшему (см. [«Доступ к боту»](#доступ-к-боту)), но всё равно не публикуйте username — до первого `/start` бот открыт для того, кто напишет первым.



### 2. Установка на OpenWrt

**Автоматически:**

```bash
wget -qO- https://raw.githubusercontent.com/bpGusar/signbox-custom-lists-tgbot/main/install.sh | sh
```

`install.sh` скачивает `.ipk` из GitHub Release (приоритет), иначе из `dist/ipk/` в репозитории.

**Сборка .ipk** (Docker, `Dockerfile-ipk` + OpenWrt SDK):

```bash
make ipk                    # все архитектуры
make ipk-aarch64            # только aarch64
TARGETS=mipsel_24kc make ipk
```

Пакеты: `dist/ipk/<архитектура>/`.

Версия назначается автоматически: `0.YYYYMMDD.<номер_сборки>` (например `0.20260611.42`).  
При каждом push в `main` GitHub Actions собирает `.ipk`, создаёт Release и обновляет `dist/ipk/`.



Ручной запуск: **Actions → Build IPK packages → Run workflow**.

### 3. Настройка в LuCI

**Services → Lists Telegram Bot**

- **Enabled** — включить бота
- **Bot Token** — токен от BotFather
- **Domain list file** — путь к `domain_list.lst` (по умолчанию `/etc/lst-signbox-lists-tgbot/domain_list.lst`)
- **IP/CIDR list file** — путь к `ip_list.lst` (по умолчанию `/etc/lst-signbox-lists-tgbot/ip_list.lst`)

Сохранить и применить.

### 4. Telegram

Отправьте боту `/start`, затем список:

```
test.com, example.org
```

или

```
1.1.1.1, 10.0.0.0/8
```



## Доступ к боту

У бота нет ролей/белого списка — управлять списками и перезапуском может любой, кто сможет ему написать. Чтобы это не превращалось в «кто первый нашёл username, тот и рулит маршрутизацией», бот **привязывается к первому чату**, который отправит ему любое сообщение:

- Первый `/start` (или любое другое сообщение) после установки/обновления сохраняет `chat_id` отправителя как владельца в `state.json` (`owner_chat_id`).
- Все остальные чаты получают отказ («🔒 Этот бот уже привязан к другому пользователю») и не могут ни читать, ни менять списки.
- При обновлении с версии без этой проверки владельцем станет тот, кто напишет боту первым **после** обновления — если это важно, напишите боту сразу после `opkg install`/перезапуска сервиса.

**Сброс привязки** (например, чтобы передать бота другому человеку или переустановить): остановите бота, удалите поле `owner_chat_id` из файла состояния (по умолчанию `/var/lib/lst-signbox-lists-tgbot/state.json`, путь настраивается через `state_path`) или файл целиком, затем запустите бота снова — следующий написавший станет новым владельцем.

## Формат файлов

```
test.com
example.org
// disabled.com
```

Отключённая запись: `//`  + значение (префикс `//` и пробел).

## Перезапуск сервиса (опционально)

Через UCI (`/etc/config/lst-signbox-lists-tgbot`):

```
option restart_cmd '/etc/init.d/your-service restart'
option service_label 'маршрутизацию'
```

После изменения списков бот покажет предупреждение и кнопку перезапуска.

## Обновление из чата

Раз в 6 часов бот проверяет GitHub Releases и присылает владельцу сообщение,
когда выходит новая версия (о каждой версии — один раз).

Обновиться можно там же: «Настройки» → «Обновить бота». Бот покажет
установленную и доступную версии, а по подтверждению запустит
`lst-signbox-lists-tgbot-upgrade start` в фоне.

Установка заканчивается перезапуском самого бота, поэтому он запоминает
операцию в `state.json` и дописывает результат в то же сообщение уже после
перезапуска. Во время установки бот недоступен около минуты.

Кнопка появляется, только если в системе есть
`/usr/sbin/lst-signbox-lists-tgbot-upgrade`. Обновление доступно владельцу
бота — тому чату, который привязан в `state.json`.

## Логи операций

По умолчанию бот пишет журнал действий в файл:

```bash
/etc/lst-signbox-lists-tgbot/logs/bot.log
```

Путь можно изменить через UCI:

```bash
uci set lst-signbox-lists-tgbot.main.log_path='/etc/lst-signbox-lists-tgbot/logs/bot.log'
uci commit lst-signbox-lists-tgbot
/etc/init.d/lst-signbox-lists-tgbot restart
```

Полезные команды на роутере:

```bash
# Последние 100 строк
tail -n 100 /etc/lst-signbox-lists-tgbot/logs/bot.log

# Смотреть в реальном времени
tail -f /etc/lst-signbox-lists-tgbot/logs/bot.log
```

Примеры событий: `/start`, разбор входного списка, нажатия кнопок, добавление/удаление/отключение записей, скачивание файлов, запуск и результат перезапуска сервиса, ошибки.

## Локальная разработка

Бот можно запускать на ПК без OpenWrt: конфигурация берётся из переменных окружения (UCI не нужен).  
Списки и логи по умолчанию пишутся в папку `testdata/` в корне репозитория.

**Требования:** Go 1.22+, токен тестового бота от [@BotFather](https://t.me/BotFather).  
Для разработки лучше отдельный бот — не запускайте локально бота с тем же токеном, что на роутере.

### Windows

Скрипт проверяет окружение, создаёт `testdata/`, настраивает переменные и предлагает запустить бота:

```powershell
.\scripts\dev-windows.ps1
```

Токен можно сохранить в `.env.local` (файл в `.gitignore`). При повторном запуске скрипт подхватит его автоматически.

### Linux / macOS

```bash
export LST_SIGNBOX_LISTS_TGBOT_TOKEN="your-token"
export LST_SIGNBOX_LISTS_TGBOT_DOMAIN_LIST="/tmp/domain_list.lst"
export LST_SIGNBOX_LISTS_TGBOT_IP_LIST="/tmp/ip_list.lst"
export LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD="echo restart"
export LST_SIGNBOX_LISTS_TGBOT_STATE_PATH="/tmp/lst-signbox-lists-tgbot-state.json"
export LST_SIGNBOX_LISTS_TGBOT_LOG_PATH="/tmp/lst-signbox-lists-tgbot.log"

make build
./lst-signbox-lists-tgbot
```



### Тестирование

В Telegram: `/start`, затем список доменов или IP. Логи — `testdata/bot.log` (Windows) или путь из `LST_SIGNBOX_LISTS_TGBOT_LOG_PATH`.

Unit-тесты без Telegram:

```bash
go test ./...
```



### После изменений в коде

Запущенный бинарник не подхватывает правки сам — нужно пересобрать и перезапустить:

```bash
go build -trimpath -o lst-signbox-lists-tgbot ./cmd/lst-signbox-lists-tgbot   # Linux/macOS
go build -trimpath -o lst-signbox-lists-tgbot.exe .\cmd\lst-signbox-lists-tgbot  # Windows
```

Остановка работающего бота: `Ctrl+C`. На Windows можно снова запустить `.\scripts\dev-windows.ps1`.

## Структура

```
cmd/lst-signbox-lists-tgbot/          — точка входа
internal/bot/          — Telegram handlers
internal/lists/        — работа с .lst файлами
internal/config/       — UCI / env
internal/service/      — state.json и перезапуск
openwrt/lists-tg/      — пакет OpenWrt (pkg: lst-signbox-lists-tgbot)
openwrt/luci-app-lists-tg/ — LuCI (pkg: luci-app-lst-signbox-lists-tgbot)
```



## Лицензия

MIT