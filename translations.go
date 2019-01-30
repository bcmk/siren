package main

type translation struct {
	Str   string    `json:"str"`
	Parse parseKind `json:"parse"`
}

type translations struct {
	Help           *translation `json:"help"`
	Online         *translation `json:"online"`
	Offline        *translation `json:"offline"`
	SyntaxAdd      *translation `json:"syntax_add"`
	SyntaxRemove   *translation `json:"syntax_remove"`
	SyntaxFeedback *translation `json:"syntax_feedback"`
	InvalidSymbols *translation `json:"invalid_symbols"`
	AlreadyAdded   *translation `json:"already_added"`
	MaxModels      *translation `json:"max_models"`
	AddError       *translation `json:"add_error"`
	ModelAdded     *translation `json:"model_added"`
	ModelNotInList *translation `json:"model_not_in_list"`
	ModelRemoved   *translation `json:"model_removed"`
	Donation       *translation `json:"donation"`
	Feedback       *translation `json:"feedback"`
	SourceCode     *translation `json:"source_code"`
	UnknownCommand *translation `json:"unknown_command"`
	Slash          *translation `json:"slash"`
	Languages      *translation `json:"languages"`
	Version        *translation `json:"version"`
	Removed        *translation `json:"removed"`
	NoModels       *translation `json:"no_models"`
}

var (
	trRu = translations{
		Online:         &translation{Str: "%s в сети", Parse: raw},
		Offline:        &translation{Str: "%s не в сети", Parse: raw},
		SyntaxAdd:      &translation{Str: "Формат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера", Parse: html},
		SyntaxRemove:   &translation{Str: "Формат команды: /remove <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера", Parse: html},
		SyntaxFeedback: &translation{Str: "Формат команды: /feedback <i>сообщение</i>", Parse: html},
		InvalidSymbols: &translation{Str: "Идентификатор модели %s содержит неподдерживаемые символы", Parse: raw},
		AlreadyAdded:   &translation{Str: "Модель %s уже в вашем списке", Parse: raw},
		MaxModels:      &translation{Str: "Можно добавить не более %d моделей", Parse: raw},
		AddError:       &translation{Str: "Не получилось добавить модель %s\nПроверьте идентификатор модели или попробуйте позже\nФормат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера", Parse: html},
		ModelAdded:     &translation{Str: "Модель %s добавлена", Parse: raw},
		ModelNotInList: &translation{Str: "Модель %s не в вашем списке", Parse: raw},
		ModelRemoved:   &translation{Str: "Модель %s удалена", Parse: raw},
		Donation:       &translation{Str: "Хотите поддержать проект?\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nЕсли вы не знаете, что это такое, просто подарите моей любимой модели BBWebb 77тк", Parse: raw},
		Feedback:       &translation{Str: "Спасибо за отклик!", Parse: raw},
		SourceCode:     &translation{Str: "Исходный код: https://github.com/bcmk/siren", Parse: raw},
		UnknownCommand: &translation{Str: "Такой команде не обучен", Parse: raw},
		Slash:          &translation{Str: "Команда выглядит так: /<i>команда</i>", Parse: html},
		Languages:      &translation{Str: "English bot: t.me/BongacamsOnlineBot", Parse: raw},
		Version:        &translation{Str: "Версия: %s", Parse: raw},
		Removed:        &translation{Str: "Модель %s удалена, её профиль не найден", Parse: raw},
		NoModels:       &translation{Str: "Вы не подписаны ни на одну модель", Parse: raw},
		Help: &translation{Str: `Бот сообщит, когда твоя любимая модель появится в сети BongaCams.

Команды

<b>add</b> <i>идентификатор модели</i> — Добавить модель
<b>remove</b> <i>идентификатор модели</i> — Удалить модель
<b>list</b> — Список подписок
<b>donate</b> — Поддержать проект
<b>feedback</b> <i>текст</i> — Обратная связь
<b>source</b> — Исходный код
<b>language</b> — Этот бот на других языках
<b>help</b> — Список команд

Идентификатор модели можно посмотреть в адресной строке браузера`, Parse: html},
	}
	trEn = translations{
		Online:         &translation{Str: "%s online", Parse: raw},
		Offline:        &translation{Str: "%s offline", Parse: raw},
		SyntaxAdd:      &translation{Str: "Syntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser", Parse: html},
		SyntaxRemove:   &translation{Str: "Syntax: /remove <i>model ID</i>\nYou can find a model ID in an address line of your browser", Parse: html},
		SyntaxFeedback: &translation{Str: "Syntax: /feedback <i>message</i>", Parse: html},
		InvalidSymbols: &translation{Str: "Model ID %s has invalid symbols", Parse: raw},
		AlreadyAdded:   &translation{Str: "Model %s is already in your list", Parse: raw},
		MaxModels:      &translation{Str: "You can add no more than %d models", Parse: raw},
		AddError:       &translation{Str: "Could not add a model %s\nCheck a model ID or try later\nSyntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser", Parse: html},
		ModelAdded:     &translation{Str: "Model %s added successfully", Parse: raw},
		ModelNotInList: &translation{Str: "Model %s is not in your list", Parse: raw},
		ModelRemoved:   &translation{Str: "Model %s removed successfully", Parse: raw},
		Donation:       &translation{Str: "Donations\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nIf you don't know what it is, just give to my favorite model BBWebb 77tkn", Parse: raw},
		Feedback:       &translation{Str: "Thank you for your feedback!", Parse: raw},
		SourceCode:     &translation{Str: "Source code: https://github.com/bcmk/siren", Parse: raw},
		UnknownCommand: &translation{Str: "Unknown command", Parse: raw},
		Slash:          &translation{Str: "Command looks like this: /<i>command</i>", Parse: html},
		Languages:      &translation{Str: "Русский бот: t.me/BongacamsSirenBot", Parse: raw},
		Version:        &translation{Str: "Version: %s", Parse: raw},
		Removed:        &translation{Str: "Profile of model %s has been removed", Parse: raw},
		NoModels:       &translation{Str: "You are not subscribed to any model", Parse: raw},
		Help: &translation{Str: `The bot notifies you when your favorite BongaCams models are online.

Commands

<b>add</b> <i>model ID</i> — Add model
<b>remove</b> <i>model ID</i> — Remove model
<b>list</b> — Subscriptions list
<b>donate</b> — Donation instructions
<b>feedback</b> <i>text</i> — Send feedback
<b>source</b> — Show source code
<b>language</b> — This bot in other languages
<b>help</b> — Command list

You can find a model ID in an address line of your browser.`, Parse: html},
	}
)
