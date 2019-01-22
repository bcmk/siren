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
	trNoModels
)

type translation struct {
	str   string
	parse parseKind
}

var (
	langRu = []translation{
		trOnline:         {str: "%s в сети", parse: raw},
		trOffline:        {str: "%s не в сети", parse: raw},
		trSyntaxAdd:      {str: "Формат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера", parse: html},
		trSyntaxRemove:   {str: "Формат команды: /remove <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера", parse: html},
		trSyntaxFeedback: {str: "Формат команды: /feedback <i>сообщение</i>", parse: html},
		trInvalidSymbols: {str: "Идентификатор модели %s содержит неподдерживаемые символы", parse: raw},
		trAlreadyAdded:   {str: "Модель %s уже в вашем списке", parse: raw},
		trMaxModels:      {str: "Можно добавить не более %d моделей", parse: raw},
		trAddError:       {str: "Не получилось добавить модель %s\nПроверьте идентификатор модели или попробуйте позже\nФормат команды: /add <i>идентификатор модели</i>\nИдентификатор модели можно посмотреть в адресной строке браузера", parse: html},
		trModelAdded:     {str: "Модель %s добавлена", parse: raw},
		trModelNotInList: {str: "Модель %s не в вашем списке", parse: raw},
		trModelRemoved:   {str: "Модель %s удалена", parse: raw},
		trDonation:       {str: "Хотите поддержать проект?\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nЕсли вы не знаете, что это такое, просто подарите моей любимой модели BBWebb 77тк", parse: raw},
		trFeedback:       {str: "Спасибо за отклик!", parse: raw},
		trSourceCode:     {str: "Исходный код: https://github.com/bcmk/bcb", parse: raw},
		trUnknownCommand: {str: "Такой команде не обучен", parse: raw},
		trSlash:          {str: "Команда выглядит так: /<i>команда</i>", parse: html},
		trLanguages:      {str: "English bot: t.me/BongacamsOnlineBot", parse: raw},
		trVersion:        {str: "Версия: %s", parse: raw},
		trRemoved:        {str: "Модель %s удалена, её профиль не найден", parse: raw},
		trNoModels:       {str: "Вы не подписаны ни на одну модель", parse: raw},
		trHelp: {str: `Бот сообщит, когда твоя любимая модель появится в сети BongaCams.

Команды

<b>add</b> <i>идентификатор модели</i> — Добавить модель
<b>remove</b> <i>идентификатор модели</i> — Удалить модель
<b>list</b> — Список подписок
<b>donate</b> — Поддержать проект
<b>feedback</b> <i>текст</i> — Обратная связь
<b>source</b> — Исходный код
<b>language</b> — Этот бот на других языках
<b>help</b> — Список команд

Идентификатор модели можно посмотреть в адресной строке браузера`, parse: html},
	}
	langEn = []translation{
		trOnline:         {str: "%s online", parse: raw},
		trOffline:        {str: "%s offline", parse: raw},
		trSyntaxAdd:      {str: "Syntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser", parse: html},
		trSyntaxRemove:   {str: "Syntax: /remove <i>model ID</i>\nYou can find a model ID in an address line of your browser", parse: html},
		trSyntaxFeedback: {str: "Syntax: /feedback <i>message</i>", parse: html},
		trInvalidSymbols: {str: "Model ID %s has invalid symbols", parse: raw},
		trAlreadyAdded:   {str: "Model %s is already in your list", parse: raw},
		trMaxModels:      {str: "You can add no more than %d models", parse: raw},
		trAddError:       {str: "Could not add a model %s\nCheck a model ID or try later\nSyntax: /add <i>model ID</i>\nYou can find a model ID in an address line of your browser", parse: html},
		trModelAdded:     {str: "Model %s added successfully", parse: raw},
		trModelNotInList: {str: "Model %s is not in your list", parse: raw},
		trModelRemoved:   {str: "Model %s removed successfully", parse: raw},
		trDonation:       {str: "Donations\nBitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE\nEthereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc\nIf you don't know what it is, just give to my favorite model BBWebb 77tkn", parse: raw},
		trFeedback:       {str: "Thank you for your feedback!", parse: raw},
		trSourceCode:     {str: "Source code: https://github.com/bcmk/bcb", parse: raw},
		trUnknownCommand: {str: "Unknown command", parse: raw},
		trSlash:          {str: "Command looks like this: /<i>command</i>", parse: html},
		trLanguages:      {str: "Русский бот: t.me/BongacamsSirenBot", parse: raw},
		trVersion:        {str: "Version: %s", parse: raw},
		trRemoved:        {str: "Profile of model %s has been removed", parse: raw},
		trNoModels:       {str: "You are not subscribed to any model", parse: raw},
		trHelp: {str: `The bot notifies you when your favorite BongaCams models are online.

Commands

<b>add</b> <i>model ID</i> — Add model
<b>remove</b> <i>model ID</i> — Remove model
<b>list</b> — Subscriptions list
<b>donate</b> — Donation instructions
<b>feedback</b> <i>text</i> — Send feedback
<b>source</b> — Show source code
<b>language</b> — This bot in other languages
<b>help</b> — Command list

You can find a model ID in an address line of your browser.`, parse: html},
	}
)
