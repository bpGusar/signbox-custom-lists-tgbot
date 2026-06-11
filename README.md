# lst-signbox-lists-tgbot

Лёгкий Telegram-бот для OpenWrt 24.10: управление файлами `domain_list.lst` и `ip_list.lst` через inline-кнопки.

## Возможности

- Приём списка доменов или IP/CIDR (через запятую, пробел или с новой строки)
- Автоопределение типа (домен / IP)
- Действия: добавить, удалить, отключить (`// value`), отмена
- Умная обработка уже существующих и отключённых записей
- Проверка файлов при `/start`, создание при отсутствии
- Предупреждение о несинхронизированном перезапуске сервиса + кнопка перезапуска
- Настройка через LuCI: токен бота и пути к файлам

## Быстрый старт

### 1. Создайте бота в Telegram

1. Откройте [@BotFather](https://t.me/BotFather)
2. `/newbot` → задайте имя и username
3. Скопируйте токен

> Не публикуйте username бота — любой, кто его найдёт, сможет менять списки.

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

## Формат файлов

```
test.com
example.org
// disabled.com
```

Отключённая запись: `// ` + значение (префикс `//` и пробел).

## Перезапуск сервиса (опционально)

Через UCI (`/etc/config/lst-signbox-lists-tgbot`):

```
option restart_cmd '/etc/init.d/your-service restart'
option service_label 'маршрутизацию'
```

После изменения списков бот покажет предупреждение и кнопку перезапуска.

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
