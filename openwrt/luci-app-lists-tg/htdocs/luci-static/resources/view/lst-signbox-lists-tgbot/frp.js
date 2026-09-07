'use strict';
'require view';
'require form';
'require uci';
'require fs';
'require poll';
'require ui';

var SETUP_SCRIPT = '/usr/sbin/lst-frp-setup';
var SETUP_LOG = '/tmp/lst-frp-setup.log';
var UCI_PACKAGE = 'lst-signbox-lists-tgbot';
var UCI_SECTION = 'main';
var STATUS_POLL_INTERVAL = 3;

var statusEl = null;
var logEl = null;
var ensureBtn = null;
var removeBtn = null;
var pollFn = null;
var polling = false;

function parseJson(res) {
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

function stateLabel(info) {
	switch (info.state) {
		case 'ok':             return _('Configured and running');
		case 'disabled':       return _('Disabled (frp is off)');
		case 'not_configured': return _('Server address or token missing');
		case 'error':          return _('Error: %s').format(info.detail || '?');
		case 'no_space':       return _('Not enough flash space: %s').format(info.detail || '?');
		case 'unsupported':    return _('Router architecture not supported: %s').format(info.detail || '?');
		case 'removed':        return _('Removed');
		case '':               return _('Never run yet');
		default:               return _('Running…');
	}
}

function renderStatus(info) {
	if (!statusEl)
		return;

	if (!info) {
		statusEl.textContent = _('Could not read frp status.');
		return;
	}

	var freeMB = Math.floor((info.free_kb || 0) / 1024);
	var lines = [
		_('State: %s').format(stateLabel(info)),
		_('frp enabled: %s').format(info.enabled ? _('yes') : _('no')),
		_('Architecture: %s').format((info.arch || '?') + (info.asset ? ' (' + info.asset + ')' : '')),
		_('Free flash space: %d MB%s').format(freeMB, info.free_ok ? '' : ' ' + _('(need >= 25 MB)')),
		_('frpc binary: %s').format(info.installed_version || _('not installed')),
		_('frpc service: %s').format(info.running ? _('running') : _('stopped'))
	];

	if (info.state === 'ok' && info.luci_ready && info.luci_domain)
		lines.push(_('LuCI URL: https://%s:8443/').format(info.luci_domain));

	statusEl.textContent = lines.join('\n');

	if (ensureBtn)
		ensureBtn.disabled = !info.enabled;
}

function fetchLog() {
	return L.resolveDefault(fs.read(SETUP_LOG), '').then(function (data) {
		if (logEl)
			logEl.value = data ? String(data).trim() : '';
	});
}

function runStatus() {
	return fs.exec(SETUP_SCRIPT, ['status']).then(function (res) {
		var info = parseJson(res);
		renderStatus(info);
		return info;
	}).catch(function () {
		renderStatus(null);
		return null;
	});
}

function pollStatus() {
	return fetchLog().then(runStatus).then(function (info) {
		if (!info)
			return info;
		if (info.state === '' || info.state === 'ok' || info.state === 'disabled' ||
			info.state === 'not_configured' || info.state === 'error' ||
			info.state === 'no_space' || info.state === 'unsupported' || info.state === 'removed') {
			stopPolling();
		}
		return info;
	});
}

function beginPolling() {
	if (polling)
		return;
	polling = true;
	pollFn = function () { return pollStatus(); };
	poll.add(pollFn, STATUS_POLL_INTERVAL);
	pollStatus();
}

function stopPolling() {
	if (!polling || !pollFn)
		return;
	poll.remove(pollFn);
	pollFn = null;
	polling = false;
}

function runEnsure() {
	if (!confirm(_('Download frpc if needed, write the config and (re)start the tunnel?')))
		return Promise.resolve();

	ensureBtn.disabled = true;
	return uci.save().then(function () {
		return uci.apply();
	}).then(function () {
		return fs.exec(SETUP_SCRIPT, ['ensure']);
	}).then(function () {
		ui.addNotification(null, E('p', {}, _('Reconcile started.')), 'info');
		beginPolling();
	}).catch(function () {
		ui.addNotification(null, E('p', {}, _('Failed to start the reconcile.')), 'danger');
	}).finally(function () {
		ensureBtn.disabled = false;
	});
}

function runRemove() {
	if (!confirm(_('Stop frpc and delete its binary and config? The bot and internet keep working.')))
		return Promise.resolve();

	removeBtn.disabled = true;
	// Also clear the flag, otherwise the bot's next self-update would reinstall
	// frpc from its postinst.
	uci.set(UCI_PACKAGE, UCI_SECTION, 'frp_enabled', '0');
	return uci.save().then(function () {
		return uci.apply();
	}).then(function () {
		return fs.exec(SETUP_SCRIPT, ['remove']);
	}).then(function () {
		ui.addNotification(null, E('p', {}, _('frpc removed and frp disabled.')), 'info');
		return pollStatus();
	}).catch(function () {
		ui.addNotification(null, E('p', {}, _('Failed to remove frpc.')), 'danger');
	}).finally(function () {
		removeBtn.disabled = false;
	});
}

function buildStatusSection() {
	statusEl = E('div', { 'class': 'cbi-value-description', 'style': 'white-space: pre-line;' }, [_('Loading…')]);
	logEl = E('textarea', {
		'class': 'cbi-input-textarea',
		'readonly': 'readonly',
		'wrap': 'off',
		'style': 'width: 100%; min-height: 200px; font-family: monospace; resize: vertical;'
	}, ['']);

	ensureBtn = E('button', { 'class': 'btn cbi-button-apply', 'type': 'button', 'click': runEnsure },
		_('Install / apply'));
	removeBtn = E('button', { 'class': 'btn cbi-button-negative', 'type': 'button', 'click': runRemove },
		_('Remove frpc'));

	return E('div', { 'class': 'cbi-section' }, [
		E('h3', {}, _('Tunnel status')),
		E('div', { 'class': 'cbi-section-descr' }, _(
			'frpc is an outbound-only client. A bad config only stops the tunnel — it never affects routing, ' +
			'the firewall or SSH, and the bot stays reachable over its own connection.'
		)),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Status')),
			E('div', { 'class': 'cbi-value-field' }, statusEl)
		]),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Actions')),
			E('div', { 'class': 'cbi-value-field' }, [ensureBtn, ' ', removeBtn])
		]),
		E('div', { 'class': 'cbi-value' }, [
			E('label', { 'class': 'cbi-value-title' }, _('Setup log')),
			E('div', { 'class': 'cbi-value-field' }, logEl)
		])
	]);
}

return view.extend({
	load: function () {
		return uci.load(UCI_PACKAGE);
	},

	render: function () {
		var m, s, o;

		m = new form.Map(UCI_PACKAGE, _('Remote access (frp)'), _(
			'Reach this router from a VPS running frps, without a public IP or port forwarding. ' +
			'Values are validated again by lst-frp-setup before they are written into the frpc config.'
		));

		s = m.section(form.NamedSection, UCI_SECTION, 'main', _('frp client'));

		o = s.option(form.Flag, 'frp_enabled', _('Enable frp'));
		o.default = '0';
		o.rmempty = false;

		o = s.option(form.Value, 'frp_version', _('frp version'));
		o.default = '0.71.0';
		o.rmempty = false;

		o = s.option(form.Value, 'frp_server_addr', _('VPS address'), _('IPv4 or hostname of the VPS running frps.'));
		o.datatype = 'host';

		o = s.option(form.Value, 'frp_server_port', _('frps port'));
		o.datatype = 'port';
		o.default = '12243';

		o = s.option(form.Value, 'frp_token', _('frp token'));
		o.password = true;

		o = s.option(form.Value, 'frp_ssh_remote_port', _('Public SSH port'), _('Must be inside the port range frps allows (4640–4643).'));
		o.datatype = 'range(4640,4643)';
		o.default = '4640';

		o = s.option(form.Value, 'frp_luci_domain', _('LuCI domain'), _('Domain that resolves to the VPS; leave the LuCI fields empty to skip the web proxy.'));

		o = s.option(form.Value, 'frp_luci_user', _('LuCI basic-auth user'));

		o = s.option(form.Value, 'frp_luci_password', _('LuCI basic-auth password'));
		o.password = true;

		return m.render().then(function (mapNode) {
			mapNode.appendChild(buildStatusSection());
			runStatus().then(function (info) {
				fetchLog();
				if (info && info.state !== '' && info.state !== 'ok' && info.state !== 'disabled' &&
					info.state !== 'not_configured' && info.state !== 'error' &&
					info.state !== 'no_space' && info.state !== 'unsupported' && info.state !== 'removed')
					beginPolling();
			});
			return mapNode;
		});
	}
});
