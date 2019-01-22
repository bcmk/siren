package main

type translationKey int

const (
	trHelp translationKey = iota
	trOnline
	trOffline
	trSyntaxAdd
	trSyntaxRemove
	trSyntaxFeedback
	trInvalidSymbols
	trAlreadyAdded
	trMaxModels
	trAddError
	trModelAdded
	trModelNotInList
	trModelRemoved
	trDonation
	trFeedback
	trSourceCode
	trUnknownCommand
	trSlash
	trLanguages
	trVersion
	trRemoved
)

var (
	langRu = []string{
		trOnline:         "%s в сети",
		trOffline:        "%s не в сети",
		trSyntaxAdd:      "Формат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера",
		trSyntaxRemove:   "Формат команды: /remove <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера",
		trSyntaxFeedback: "Формат команды: /feedback <i>сообщение</i>",
		trInvalidSymbols: "Идентификатор модели %s содержит неподдерживаемые символы",
		trAlreadyAdded:   "Модель %s уже в вашем списке",
		trMaxModels:      "Можно добавить не более %d моделей",
		trAddError:       "Не получилось добавить модель %s\nПроверьте идентификатор модели или попробуйте позже\nФормат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера",
		trModelAdded:     "Модель %s добавлена",
		trModelNotInList: "Модель %s не в вашем списке",
		trModelRemoved:   "Модель %s удалена",
		trDonation:       "Хотите поддержать проект?\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nЕсли вы не знаете, что это такое, просто подарите моей любимой модели BBWebb 77тк",
		trFeedback:       "Спасибо за отклик!",
		trSourceCode:     "Исходный код: https://github.com/bcmk/bcb",
		trUnknownCommand: "Такой команде не обучен",
		trSlash:          "Команда выглядит так: /<i>команда</i>",
		trLanguages:      "English bot: t.me/BongacamsOnlineBot",
		trVersion:        "Версия: %s",
		trRemoved:        "Модель %s удалена, её профиль не найден",
		trHelp: `Бот сообщит, когда твоя любимая модель появится в сети BongaCams.

Команды

<b>add</b> <i>идентификатор модели</i> — Добавить модель
<b>remove</b> <i>идентификатор модели</i> — Удалить модель
<b>list</b> — Список подписок
<b>donate</b> — Поддержать проект
<b>feedback</b> <i>текст</i> — Обратная связь
<b>source</b> — Исходный код
<b>language</b> — Этот бот на других языках
<b>help</b> — Список команд

Идентификатор модели можно посмотреть в адресной строке браузера`,
	}
	langEn = []string{
		trOnline:         "%s online",
		trOffline:        "%s offline",
		trSyntaxAdd:      "Syntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser",
		trSyntaxRemove:   "Syntax: /remove <i>model ID</i>\nYou can find a model ID in an address line of your browser",
		trSyntaxFeedback: "Syntax: /feedback <i>message</i>",
		trInvalidSymbols: "Model ID %s has invalid symbols",
		trAlreadyAdded:   "Model %s is already in your list",
		trMaxModels:      "You can add no more than %d models",
		trAddError:       "Could not add a model %s\nCheck a model ID or try later\nSyntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser",
		trModelAdded:     "Model %s added successfully",
		trModelNotInList: "Model %s is not in your list",
		trModelRemoved:   "Model %s removed successfully",
		trDonation:       "Donations\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nIf you don't know what it is, just give to my favorite model BBWebb 77tkn",
		trFeedback:       "Thank you for your feedback!",
		trSourceCode:     "Source code: https://github.com/bcmk/bcb",
		trUnknownCommand: "Unknown command",
		trSlash:          "Command looks like this: /<i>command</i>",
		trLanguages:      "Русский бот: t.me/BongacamsSirenBot",
		trVersion:        "Version: %s",
		trRemoved:        "Profile of model %s has been removed",
		trHelp: `The bot notifies you when your favorite BongaCams models are online.

Commands

<b>add</b> <i>model ID</i> — Add model
<b>remove</b> <i>model ID</i> — Remove model
<b>list</b> — Subscriptions list
<b>donate</b> — Donation instructions
<b>feedback</b> <i>text</i> — Send feedback
<b>source</b> — Show source code
<b>language</b> — This bot in other languages
<b>help</b> — Command list

You can find a model ID in an address line of your browser.`,
	}
)
