'use strict';
'require view';
'require form';
'require uci';
'require fs';
'require poll';

var LOG_TAIL_LINES = 200;
var LOG_POLL_INTERVAL = 3;

var logTextarea = null;
var pollLogFn = null;
var autoRefresh = true;
var userScrolled = false;
var scrollPosition = 0;

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
							setAutoRefresh(ev.target.checked);
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
		var m, s, o, logSection;

		m = new form.Map('lst-signbox-lists-tgbot', _('Lists Telegram Bot'), _(
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

		return m.render().then(function (mapNode) {
			mapNode.appendChild(logSection);

			if (autoRefresh)
				setAutoRefresh(true);
			else
				fetchLogs();

			return mapNode;
		});
	},
});
