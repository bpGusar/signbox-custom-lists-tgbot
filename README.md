# lists-tg

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
sh <(wget -O - https://raw.githubusercontent.com/bpGusar/signbox-custom-lists-tgbot/main/install.sh)
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
- **Domain list file** — путь к `domain_list.lst` (по умолчанию `/etc/lists-tg/domain_list.lst`)
- **IP/CIDR list file** — путь к `ip_list.lst` (по умолчанию `/etc/lists-tg/ip_list.lst`)

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

Через UCI (`/etc/config/lists-tg`):

```
option restart_cmd '/etc/init.d/your-service restart'
option service_label 'маршрутизацию'
```

После изменения списков бот покажет предупреждение и кнопку перезапуска.

## Локальная разработка

```bash
export LISTS_TG_TOKEN="your-token"
export LISTS_TG_DOMAIN_LIST="/tmp/domain_list.lst"
export LISTS_TG_IP_LIST="/tmp/ip_list.lst"
export LISTS_TG_RESTART_CMD="echo restart"
export LISTS_TG_STATE_PATH="/tmp/lists-tg-state.json"

make build
./lists-tg
```

## Структура

```
cmd/lists-tg/          — точка входа
internal/bot/          — Telegram handlers
internal/lists/        — работа с .lst файлами
internal/config/       — UCI / env
internal/service/      — state.json и перезапуск
openwrt/lists-tg/      — пакет OpenWrt
openwrt/luci-app-lists-tg/ — LuCI
```

## Лицензия

MIT
