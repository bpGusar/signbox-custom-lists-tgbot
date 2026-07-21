'use strict';
'require view';
'require form';
'require uci';
'require fs';
'require poll';
'require ui';

var LOG_TAIL_LINES = 200;
var LOG_POLL_INTERVAL = 3;
var UPGRADE_SCRIPT = '/usr/sbin/lst-signbox-lists-tgbot-upgrade';
var UPGRADE_LOG_PATH = '/tmp/lst-signbox-lists-tgbot-upgrade.log';
var UPGRADE_POLL_INTERVAL = 2;
var UCI_PACKAGE = 'lst-signbox-lists-tgbot';
var UCI_SECTION = 'main';
var UCI_LOG_AUTO_REFRESH = 'log_auto_refresh';

var logTextarea = null;
var pollLogFn = null;
var autoRefresh = true;
var userScrolled = false;
var scrollPosition = 0;

var upgradeStatusEl = null;
var upgradeLogEl = null;
var upgradeCheckBtn = null;
var upgradeStartBtn = null;
var upgradePollFn = null;
var upgradePolling = false;

function getLogPath() {
	return uci.get('lst-signbox-lists-tgbot', 'main', 'log_path') ||
		'/etc/lst-signbox-lists-tgbot/logs/bot.log';
}

function updateLogView(data) {
	if (!logTextarea)
		return;

	logTextarea.value = data || _('No log data yet.');

	if (!userScrolled)
		logTextarea.scrollTop = logTextarea.scrollHeight;
	else
		logTextarea.scrollTop = scrollPosition;
}

function fetchLogs() {
	var path = getLogPath();

	return L.resolveDefault(
		fs.exec_direct('tail', ['-n', String(LOG_TAIL_LINES), path]),
		''
	).then(function (res) {
		updateLogView((res && res.trim()) ? res.trim() : null);
	});
}

function setAutoRefresh(enabled) {
	autoRefresh = enabled;

	if (!pollLogFn)
		return;

	if (enabled) {
		fetchLogs();
		poll.add(pollLogFn, LOG_POLL_INTERVAL);
	} else {
		poll.remove(pollLogFn);
	}
}

function parseStoredFlag(value, fallback) {
	if (value == null || value === '')
		return fallback;

	value = String(value).toLowerCase();
	return value === '1' || value === 'true' || value === 'yes' || value === 'on';
}

function loadAutoRefreshSetting() {
	return parseStoredFlag(uci.get(UCI_PACKAGE, UCI_SECTION, UCI_LOG_AUTO_REFRESH), true);
}

function persistAutoRefreshSetting(enabled) {
	uci.set(UCI_PACKAGE, UCI_SECTION, UCI_LOG_AUTO_REFRESH, enabled ? '1' : '0');
	return uci.save().then(function () {
		return uci.apply();
	});
}

function parseUpgradeJson(res) {
	var text;

	if (!res)
		return null;

	if (typeof res === 'string')
		text = res.trim();
	else if (res.stdout)
		text = String(res.stdout).trim();
	else
		return null;

	if (!text)
		return null;

	try {
		return JSON.parse(text);
	} catch (e) {
		return null;
	}
}

function renderUpgradeStatus(info) {
	if (!upgradeStatusEl)
		return;

	if (!info) {
		upgradeStatusEl.textContent = _('Could not check for updates.');
		if (upgradeStartBtn)
			upgradeStartBtn.disabled = true;
		if (upgradeCheckBtn)
			upgradeCheckBtn.disabled = false;
		return;
	}

	var lines = [];
	lines.push(_('Installed version: %s').format(info.current || _('unknown')));

	if (info.latest)
		lines.push(_('Latest version: %s').format(info.latest));
	else
		lines.push(_('Could not fetch the latest version from GitHub.'));

	if (info.running || info.state === 'running')
		lines.push(_('Update in progress…'));
	else if (info.state === 'success')
		lines.push(_('Last update completed successfully.'));
	else if (info.state === 'failed')
		lines.push(_('Last update failed. See log below.'));
	else if (info.update_available)
		lines.push(_('A newer version is available.'));
	else if (info.latest)
		lines.push(_('You are on the latest version.'));

	upgradeStatusEl.textContent = lines.join('\n');

	if (upgradeStartBtn)
		upgradeStartBtn.disabled = !!(info.running || info.state === 'running') || !info.update_available;

	if (upgradeCheckBtn)
		upgradeCheckBtn.disabled = !!(info.running || info.state === 'running');
}

function fetchUpgradeLog() {
	return L.resolveDefault(fs.read(UPGRADE_LOG_PATH), '').then(function (data) {
		if (upgradeLogEl)
			upgradeLogEl.value = data ? String(data).trim() : '';
	});
}

function runUpgradeCheck() {
	return fs.exec(UPGRADE_SCRIPT, ['check']).then(function (res) {
		var info = parseUpgradeJson(res);
		renderUpgradeStatus(info);
		return info;
	}).catch(function () {
		renderUpgradeStatus(null);
		return null;
	});
}

function pollUpgradeStatus() {
	return fetchUpgradeLog().then(function () {
		return fs.exec(UPGRADE_SCRIPT, ['status']);
	}).then(function (res) {
		var info = parseUpgradeJson(res);

		renderUpgradeStatus(info);

		if (!info)
			return info;

		if (info.running || info.state === 'running')
			return info;

		stopUpgradePolling();

		if (info.state === 'success') {
			ui.addNotification(null, E('p', {}, _(
				'Update completed successfully. Reload this page if the interface looks outdated.'
			)), 'success');
			return runUpgradeCheck();
		}

		if (info.state === 'failed')
			ui.addNotification(null, E('p', {}, _('Update failed. See log below.')), 'danger');

		return info;
	});
}

function beginUpgradePolling() {
	if (upgradePolling)
		return;

	upgradePolling = true;
	upgradePollFn = function () {
		return pollUpgradeStatus();
	};
	poll.add(upgradePollFn, UPGRADE_POLL_INTERVAL);
	pollUpgradeStatus();
}

function stopUpgradePolling() {
	if (!upgradePolling || !upgradePollFn)
		return;

	poll.remove(upgradePollFn);
	upgradePollFn = null;
	upgradePolling = false;
}

function startUpgrade() {
	if (!confirm(_('Download and install the latest package from GitHub?')))
		return Promise.resolve();

	upgradeStartBtn.disabled = true;

	return fs.exec(UPGRADE_SCRIPT, ['start']).then(function (res) {
		var info = parseUpgradeJson(res);

		if (!info || info.error) {
			ui.addNotification(null, E('p', {}, info && info.error
				? _('Update is already running.')
				: _('Failed to start update.')), 'danger');
			return runUpgradeCheck();
		}

		beginUpgradePolling();
		return pollUpgradeStatus();
	}).catch(function () {
		ui.addNotification(null, E('p', {}, _('Failed to start update.')), 'danger');
		return runUpgradeCheck();
	});
}

function buildUpgradeSection() {
	upgradeStatusEl = E('div', {
		'class': 'cbi-value-description',
		'style': 'white-space: pre-line;'
	}, [_('Checking for updates...')]);

	upgradeLogEl = E('textarea', {
		'class': 'cbi-input-textarea',
		'readonly': 'readonly',
		'wrap': 'off',
		'style': 'width: 100%; min-height: 160px; font-family: monospace; resize: vertical;'
	}, ['']);

	upgradeCheckBtn = E('button', {
		'class': 'btn cbi-button-action',
		'type': 'button',
		'click': function () {
			upgradeCheckBtn.disabled = true;
			if (upgradeStatusEl)
				upgradeStatusEl.textContent = _('Checking for updates...');

			return runUpgradeCheck().then(fetchUpgradeLog).finally(function () {
				upgradeCheckBtn.disabled = false;
			});
		}
	}, _('Check for updates'));

	upgradeStartBtn = E('button', {
		'class': 'btn cbi-button-apply',
		'type': 'button',
		'disabled': 'disabled',
		'click': startUpgrade
	}, _('Install update'));

	return E('div', { 'class': 'cbi-section' }, [
		E('h3', {}, _('Software update')),
		E('div', { 'class': 'cbi-section-descr' }, _(
			'Download and install the latest bot and LuCI packages from GitHub Release.'
		)),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Status')),
			E('div', { 'class': 'cbi-value-field' }, upgradeStatusEl)
		]),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Actions')),
			E('div', { 'class': 'cbi-value-field' }, [
				upgradeCheckBtn,
				' ',
				upgradeStartBtn
			])
		]),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Update log')),
			E('div', { 'class': 'cbi-value-field' }, upgradeLogEl)
		])
	]);
}

function buildLogSection() {
	logTextarea = E('textarea', {
		'id': 'lst-signbox-lists-tgbot-log',
		'class': 'cbi-input-textarea',
		'readonly': 'readonly',
		'wrap': 'off',
		'style': 'width: 100%; min-height: 400px; max-height: 70vh; font-family: monospace; resize: vertical;'
	}, [_('Loading...')]);

	logTextarea.addEventListener('scroll', function () {
		var atBottom = logTextarea.scrollHeight - logTextarea.clientHeight - logTextarea.scrollTop < 8;

		if (atBottom) {
			userScrolled = false;
		} else {
			userScrolled = true;
			scrollPosition = logTextarea.scrollTop;
		}
	});

	pollLogFn = function () {
		if (!autoRefresh)
			return Promise.resolve();

		return fetchLogs();
	};

	return E('div', { 'class': 'cbi-section' }, [
		E('h3', {}, _('Bot log')),
		E('div', { 'class': 'cbi-section-descr' }, _(
			'Last %d lines from the bot log file configured above.'
		).format(LOG_TAIL_LINES)),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Controls')),
			E('div', { 'class': 'cbi-value-field' }, [
				E('label', { 'style': 'margin-right: 1.5em; cursor: pointer;' }, [
					E('input', {
						'type': 'checkbox',
						'checked': autoRefresh ? 'checked' : null,
						'change': function (ev) {
							var enabled = !!ev.target.checked;
							var prev = autoRefresh;

							setAutoRefresh(enabled);
							persistAutoRefreshSetting(enabled).catch(function () {
								setAutoRefresh(prev);
								ev.target.checked = prev;
								ui.addNotification(null, E('p', {}, _(
									'Failed to save auto-refresh setting. Reverted to previous value.'
								)), 'danger');
							});
						}
					}),
					' ',
					_('Auto-refresh logs')
				]),
				E('button', {
					'class': 'btn cbi-button-action',
					'type': 'button',
					'click': function () {
						fetchLogs();
					}
				}, _('Refresh now'))
			])
		]),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Log output')),
			E('div', { 'class': 'cbi-value-field' }, logTextarea)
		])
	]);
}

return view.extend({
	load: function () {
		return uci.load('lst-signbox-lists-tgbot');
	},

	render: function () {
		var m, s, o, logSection, upgradeSection;

		autoRefresh = loadAutoRefreshSetting();

		m = new form.Map(UCI_PACKAGE, _('Lists Telegram Bot'), _(
			'Telegram bot for managing domain and IP/CIDR list files.'
		));

		s = m.section(form.NamedSection, 'main', 'main', _('Settings'));

		o = s.option(form.Flag, 'enabled', _('Enabled'));
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.Value, 'token', _('Bot Token'));
		o.password = true;
		o.placeholder = '123456:ABC...';
		o.rmempty = false;

		o = s.option(form.Value, 'domain_list', _('Domain list file'));
		o.default = '/etc/lst-signbox-lists-tgbot/domain_list.lst';
		o.rmempty = false;

		o = s.option(form.Value, 'ip_list', _('IP/CIDR list file'));
		o.default = '/etc/lst-signbox-lists-tgbot/ip_list.lst';
		o.rmempty = false;

		o = s.option(form.Value, 'restart_cmd', _('Restart command'));
		o.default = '/etc/init.d/podkop restart';
		o.placeholder = '/etc/init.d/podkop restart';
		o.rmempty = false;

		o = s.option(form.Value, 'service_label', _('Service label'));
		o.default = 'podkop';
		o.placeholder = 'podkop';
		o.rmempty = false;

		o = s.option(form.Flag, 'auto_restart', _('Restart service after list changes'));
		o.default = '1';
		o.rmempty = false;

		o = s.option(form.Value, 'log_path', _('Bot log file'));
		o.default = '/etc/lst-signbox-lists-tgbot/logs/bot.log';
		o.rmempty = false;

		logSection = buildLogSection();
		upgradeSection = buildUpgradeSection();

		return m.render().then(function (mapNode) {
			mapNode.appendChild(upgradeSection);
			mapNode.appendChild(logSection);

			runUpgradeCheck().then(function (info) {
				if (info && (info.running || info.state === 'running'))
					beginUpgradePolling();
				else
					fetchUpgradeLog();
			});

			if (autoRefresh)
				setAutoRefresh(true);
			else
				fetchLogs();

			return mapNode;
		});
	},
});
