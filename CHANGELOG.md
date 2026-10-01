# Changelog

Здесь — что изменилось в каждой версии: простыми словами для человека, со ссылкой на задачу и на
изменение, которыми строка пришла в код.

Формат — [Keep a Changelog 1.1.0](https://keepachangelog.com/ru/1.1.0/), версии —
[SemVer](https://semver.org/lang/ru/). Разделы версий идут от новых к старым, а строки внутри
раздела — в том порядке, в котором изменения вливались.

**Правило.** Задача, которая меняет то, что человек увидит или сможет сделать, добавляет строку в
раздел «Не выпущено» в том же PR, где меняется поведение (правило 3 в
[PROJECT_RULES.md](PROJECT_RULES.md)). Строка пишется для человека и не копируется из заголовка
коммита; из коммитов журнал не собирается.

**Первый выпуск — `v0.1.0`**, при закрытии вехи `v1`. До него всё слитое лежит в «Не выпущено».
Как выпустить версию — в [README](README.md#releases) и в `scripts/release.sh`.

## [Не выпущено]

### Добавлено

- `crewflow task list` — список прогонов проекта: идущие сверху, а у каждого номер, число попыток,
  чем кончилась последняя, когда и как долго шла, и какое изменение она открыла.
  ([#5](https://github.com/naghuale/crewflow/pull/5), задача [#1](https://github.com/naghuale/crewflow/issues/1))
- `crewflow auth app import` и `crewflow auth app check` — внести ключ GitHub App в связку ключей
  macOS и проверить, что у установки именно те права, которые нужны прогону.
  ([#11](https://github.com/naghuale/crewflow/pull/11), задача [#3](https://github.com/naghuale/crewflow/issues/3))
- `crewflow task list` говорит, какой это проект и что ещё бежит на этой машине, а `-all` показывает
  все проекты сразу, из любой папки.
  ([#19](https://github.com/naghuale/crewflow/pull/19), задача [#10](https://github.com/naghuale/crewflow/issues/10))
- `crewflow review` — ворота слияния в коде: можно ли слить это изменение, а если нельзя — одна
  причина из таблицы §7h и что с ней делать.
  ([#24](https://github.com/naghuale/crewflow/pull/24), задача [#7](https://github.com/naghuale/crewflow/issues/7))
- Таблица списка читается с одного взгляда: колонки выровнены, заголовок не режется посреди слова,
  цвет — только в терминале, без него и в файле и в трубе остаются буквы.
  ([#27](https://github.com/naghuale/crewflow/pull/27), задача [#20](https://github.com/naghuale/crewflow/issues/20))
- Прогон, остановившийся на известной привычке (`/tmp`, папка рядом с рабочей копией), продолжается
  один раз сам, в той же сессии, с текстом о том, что делать.
  ([#30](https://github.com/naghuale/crewflow/pull/30), задача [#26](https://github.com/naghuale/crewflow/issues/26))
- `crewflow merge` — слияние ровно одобренного коммита, одним refspec, без `--force`; и
  `crewflow verify` — проверка после слияния: ветка на том коммите, задача закрыта, CI зелёный.
  ([#42](https://github.com/naghuale/crewflow/pull/42), задача [#8](https://github.com/naghuale/crewflow/issues/8))
- `CHANGELOG.md` — этот файл.
  ([#69](https://github.com/naghuale/crewflow/pull/69), задача [#69](https://github.com/naghuale/crewflow/issues/69))
- `make install SIGN_IDENTITY="<name>"` — та же установка, что `go install`, но программа подписана
  сертификатом владельца машины, и доступ к ключу переживает пересборку; без `SIGN_IDENTITY` скрипт
  говорит, чего это стоит. README рассказывает, как сделать такой сертификат.
  ([#73](https://github.com/naghuale/crewflow/pull/73), задача [#65](https://github.com/naghuale/crewflow/issues/65))
- Прогон, который стоит, виден: `task list` показывает `stalled` со временем простоя, в журнале
  прогона — по строке на вход в простой и на выход, а `crewflow task check-stalled` для расписания
  оркестратора говорит, что стоит, и оставляет под задачей одну запись на эпизод простоя. Порог —
  `[executor] stall_after`, по умолчанию 10 минут; прогон, ждущий окно связки ключей, показывает
  причину. Состояние прогона теперь пишется до похода к машине за ключом, так что прогон в этом
  ожидании виден в `~/.crewflow/state`.
  ([#88](https://github.com/naghuale/crewflow/pull/88), задача [#67](https://github.com/naghuale/crewflow/issues/67))
- Режим оркестратора: `[orchestrator] mode = "separate"` — второй GitHub App проекта, от его имени
  пишется запись ревью и пушится одобренный коммит, а решение владельца (приёмка `ACCEPTED`, `SCOPE:
  ACCEPTED`) gate считает только от `[merge] owners`, где оркестратора нет, — тем же списком, что и
  приёмка сборки (#9). Пока App не настроен, режим `shared`, и `doctor` говорит, что это стоит
  дисциплины. В режиме `separate` файл, где владелец среди `[merge] reviewers` или где один логин
  есть и в `reviewers`, и в `owners`, не читается: режим обещает разделение, которого эти аккаунты
  не делают. `crewflow doctor` печатает раздел **authority separation** — владелец, оркестратор,
  исполнитель, `owner == orchestrator` (YES/NO), пересечение списков — и держит долг доверия
  (`trust_debt`: `owner-orchestrator-overlap`, `owners-reviewers-overlap`) и в `-json`.
  ([#89](https://github.com/naghuale/crewflow/pull/89), задача [#31](https://github.com/naghuale/crewflow/issues/31))
- Карта доступа в подсказке прогона: где писать, что читать вне рабочей папки (с тем, кто попросил, и
  почему), что запрещено — и правило путей рядом с ними. Раньше исполнитель узнавал о своих правах
  только из отказа и останавливался на нём.
  ([#96](https://github.com/naghuale/crewflow/pull/96), задача [#41](https://github.com/naghuale/crewflow/issues/41))
- Поле «что читать вне рабочей папки и зачем» в задаче: одна папка сверх `[access]`, только на этот
  прогон и только на чтение, с причиной, которую видно в ревью. `crewflow task check` отказывает
  задаче, где у пути нет причины.
  ([#96](https://github.com/naghuale/crewflow/pull/96), задача [#41](https://github.com/naghuale/crewflow/issues/41))
- Один источник прав: `crewflow doctor` говорит, если в глобальном конфиге OpenCode есть таблица
  `permission`, а `crewflow task run` отказывается начинать прогон, пока она там есть. Раньше эти
  права попадали в прогон молча, мимо всего, что crewflow написал.
  ([#96](https://github.com/naghuale/crewflow/pull/96), задача [#41](https://github.com/naghuale/crewflow/issues/41))
- `crewflow doctor` показывает у каждой открытой папки, кто попросил её открыть и зачем — в строке
  отчёта и в `-json`.
  ([#96](https://github.com/naghuale/crewflow/pull/96), задача [#41](https://github.com/naghuale/crewflow/issues/41))

### Изменено

- Ворота слияния описаны спецификацией, по которой пишутся тесты: что хранится, что вычисляется, кто
  может одобрить, какие причины отказа бывают.
  ([#4](https://github.com/naghuale/crewflow/pull/4))
- Исполнитель самого crewflow работает как бот своего GitHub App, а не как владелец.
  ([#12](https://github.com/naghuale/crewflow/pull/12))
- README отвечает сначала «зачем это», потом «как это работает», и говорит честно, что ещё в работе.
  ([#16](https://github.com/naghuale/crewflow/pull/16))
- Меры процесса и бюджет CI расписаны формулами, с правилом «метрики объясняют, а не ранжируют».
  ([#23](https://github.com/naghuale/crewflow/pull/23))
- Откуда взялось правило «CI — ограниченный ресурс»: числа за сентябрь по прогонам, минутам и долям
  macOS.
  ([#25](https://github.com/naghuale/crewflow/pull/25))
- Ошибки оркестратора записаны уроками, и каждый урок закрыт контролем.
  ([#28](https://github.com/naghuale/crewflow/pull/28))
- Матрица ответственности: кто что может, чем каждая граница удерживается и где вместо проверки
  осталось доверие.
  ([#33](https://github.com/naghuale/crewflow/pull/33))
- README называет роли, зоны доступа и здоровье процесса; подробности режимов ушли в дизайн, с
  ссылкой на него.
  ([#34](https://github.com/naghuale/crewflow/pull/34))
- Сказано, что входит в ядро, и названы вехи `v1` и `v1.1`: задача в ядре только тогда, когда без неё
  ломается путь одной задачи.
  ([#35](https://github.com/naghuale/crewflow/pull/35))
- Тесты ворот слияния описаны в §7h как обещания, которые может прочитать человек, а не как номера
  тестов; реестр обещаний сверяется с разделом автоматически.
  ([#36](https://github.com/naghuale/crewflow/pull/36))
- В правилах проекта: документ — то же, что код, и его никто не вливает сам; сказано, где правда
  (репозиторий, хостинг, журнал практики); задача вне вехи ссылается на факт журнала или на пункт
  вехи.
  ([#39](https://github.com/naghuale/crewflow/pull/39), задача [#38](https://github.com/naghuale/crewflow/issues/38))
- Правило 4: дефект, переживший три неудачные проверки правки, следующей правкой не закрывается, пока
  не появился новый факт — и перечислено, что фактом считается.
  ([#46](https://github.com/naghuale/crewflow/pull/46), задача [#44](https://github.com/naghuale/crewflow/issues/44))
- `crewflow doctor` называет каждый шаг отчёта до того, как делает его, и задаёт два вопроса, на
  которые можно ответить без окна: `keychain access` — может ли эта сборка читать секреты без
  окна, и `binary signature` — что о подписи говорит `codesign`. Шаги идут в stderr, поэтому
  `-json` остаётся одним отчётом.
  ([#73](https://github.com/naghuale/crewflow/pull/73), задача [#65](https://github.com/naghuale/crewflow/issues/65))

### Исправлено

- Ответы GitHub читаются такими, какими GitHub их даёт: каждое поле сверено с документацией REST API
  версии `2022-11-28`, и неизвестный тип поля больше не молча ломает проверку.
  ([#14](https://github.com/naghuale/crewflow/pull/14), задача [#13](https://github.com/naghuale/crewflow/issues/13))
- Токен App запрашивается по имени репозитория, как это описано у GitHub, а не по `owner/name`;
  `doctor` спрашивает у App пробный токен и выбрасывает его, чтобы отказ GitHub был виден до задачи.
  ([#17](https://github.com/naghuale/crewflow/pull/17), задача [#15](https://github.com/naghuale/crewflow/issues/15))
- `verify` читает уже слитое изменение, а не то, которое ещё можно слить: слитая на другой машине
  правка больше не проходит проверку с выводом «эта машина не знала состояния задачи».
  ([#48](https://github.com/naghuale/crewflow/pull/48), задача [#43](https://github.com/naghuale/crewflow/issues/43))
- Тест наблюдения за прогоном больше не зависает, когда тик остался без читателя: тест ждёт конца
  наблюдения, а не отправки в канал.
  ([#71](https://github.com/naghuale/crewflow/pull/71), задача [#47](https://github.com/naghuale/crewflow/issues/47))
- Отсутствие правил ветки на бесплатном плане больше не значит «хостинг недоступен»: ворота берут
  проверки из `crewflow.toml` и говорят в отчёте, что правил нет.
  ([#72](https://github.com/naghuale/crewflow/pull/72), задача [#66](https://github.com/naghuale/crewflow/issues/66))
- `crewflow merge` закрывает задачу сам, когда хостинг не закрыл её по `Closes #N`, и говорит в
  отчёте, кто закрыл; при отказе ворот не закрывается ничего.
  ([#75](https://github.com/naghuale/crewflow/pull/75), задача [#70](https://github.com/naghuale/crewflow/issues/70))
- Ожидание доступа к связке ключей больше не молчит и не длится бесконечно: команда говорит, о чём
  будет ждать, ещё до ожидания — в терминале и в журнале попытки, — а через две минуты заканчивается
  как `blocked` с причиной `keychain-approval`, и попытка, у которой исполнитель не запускался, видна
  в `crewflow task list`.
  ([#73](https://github.com/naghuale/crewflow/pull/73), задача [#65](https://github.com/naghuale/crewflow/issues/65))
- Прогон, оборванный до старта исполнителя, убирает за собой рабочую копию и не оставляет её
  следующему прогону.
  ([#73](https://github.com/naghuale/crewflow/pull/73), задача [#65](https://github.com/naghuale/crewflow/issues/65))
- Проверка `secrets` больше не красит изменение из-за заглушек чужой ветки: на pull request
  сканируются только коммиты самого изменения, на `main` — история `main`, а заглушка узнаётся по
  своему значению или по пометке `placeholder` на своей строке, а не по отпечатку коммита.
  ([#92](https://github.com/naghuale/crewflow/pull/92), задача [#91](https://github.com/naghuale/crewflow/issues/91))
- В режиме `separate` ворота больше не отказывают каждому изменению: правила ветки читаются там,
  где их можно прочитать правом `Metadata`, а не `Administration`. Ворота сначала спрашивают
  сводку ветки и, если классическая защита выключена, за её правила вовсе не идут, — а если она
  включена и прочитать её нельзя, отказ остаётся и говорит, что делать: перенести правила в набор
  правил или выдать App `administration: read` (решение владельца). `crewflow doctor` говорит о
  таком раньше первого слияния, строкой `orchestrator branch protection`.
  ([#107](https://github.com/naghuale/crewflow/pull/107), задача [#102](https://github.com/naghuale/crewflow/issues/102))
- Ворота отличают App от человека больше не по строке логина: GitHub называет App в GraphQL
  одним slug (`crewflow-orchestrator`), а человек может иметь такой же логин, — и одобрение,
  написанное человеком, считалось одобрением App оркестратора (наоборот, App не проходил мимо
  `crewflow-orchestrator[bot]` в списке). Теперь автор записи читается с его видом (`Bot` или
  `User`), суффикс `[bot]` ставится по виду, а не вычитывается из логина; момент правки
  по-прежнему берётся там, где он точный, — из GraphQL. Вид аккаунта, которого crewflow не
  знает, ворота не читают как «человека»: такая запись отказывается, а не достаётся тому, кто
  носит то же имя.
  ([#107](https://github.com/naghuale/crewflow/pull/107), задача [#102](https://github.com/naghuale/crewflow/issues/102))
- Ворота сказали «можно сливать», а пуш был отказан: git читал `credential.helper = crewflow auth
  git-credential` как имя программы и искал `git-credential-crewflow`. Теперь помощник — это `!`
  и абсолютный путь той сборки, которая выполняется: пуш подписывает ровно та сборка, которая
  судила, и не зависит от `PATH`. Роль в строке своя у прогона и у слияния.
  ([#107](https://github.com/naghuale/crewflow/pull/107), задача [#102](https://github.com/naghuale/crewflow/issues/102))

### Безопасность

- Исполнитель читает только те папки, которые проект назвал командами (`[access] read_from`), и не
  может прочитать секрет — даже если проект назвал его сам.
  ([#6](https://github.com/naghuale/crewflow/pull/6), задача [#2](https://github.com/naghuale/crewflow/issues/2))
- Режим `[identity] mode = "bot"`: исполнитель работает как бот GitHub App с часовым токеном на один
  репозиторий и четырьмя правами, ключ лежит в связке ключей macOS, а `pre-push` пропускает только
  ветку задачи и не трогает рабочую копию человека.
  ([#11](https://github.com/naghuale/crewflow/pull/11), задача [#3](https://github.com/naghuale/crewflow/issues/3))
- Отказ на секрете никогда не причина продолжить прогон: он заканчивается `blocked-secret`, и
  crewflow не возобновляет такой прогон сам.
  ([#30](https://github.com/naghuale/crewflow/pull/30), задача [#26](https://github.com/naghuale/crewflow/issues/26))
- Проект crewflow работает в режиме `separate`: запись ревью и пуш одобренного коммита пишет
  App `crewflow-orchestrator` (5140522) под своим именем, а `[merge] owners = ["naghuale"]` —
  решения владельца (приёмка `ACCEPTED`, `SCOPE: ACCEPTED`, обход правила) gate считает только
  от владельца, и от оркестратора не считает ничего. `crewflow doctor` у этого проекта печатает
  `owner == orchestrator: NO` и пустой `trust_debt`.
  ([#98](https://github.com/naghuale/crewflow/pull/98), задача [#95](https://github.com/naghuale/crewflow/issues/95))

[Не выпущено]: https://github.com/naghuale/crewflow/commits/main