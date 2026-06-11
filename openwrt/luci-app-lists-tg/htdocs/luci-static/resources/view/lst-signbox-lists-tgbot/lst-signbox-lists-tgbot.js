'use strict';
'require view';
'require form';
'require uci';

return view.extend({
	load: function () {
		return uci.load('lst-signbox-lists-tgbot');
	},

	render: function () {
		var m, s, o;

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

		o = s.option(form.Value, 'log_path', _('Bot log file'));
		o.default = '/etc/lst-signbox-lists-tgbot/logs/bot.log';
		o.rmempty = false;

		return m.render();
	},
});
