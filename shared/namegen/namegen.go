package namegen

import (
	"math/rand"
	"strconv"
	"strings"
)

// Go 1.20+ auto-seeds math/rand — no need for rand.Seed()

// Generate returns a realistic email local-part.
// Mixes international standard patterns with Thai nickname culture (90s Hotmail era).
// Uses strings.Builder with pre-alloc for zero-alloc hot path.
func Generate() string {
	var b strings.Builder
	b.Grow(24)

	// 40% international standard, 60% Thai-flavored
	if rand.Intn(10) < 4 {
		genInternational(&b)
	} else {
		genThai(&b)
	}

	return b.String()
}

// ─── International patterns (temp-mail.org style) ───

func genInternational(b *strings.Builder) {
	first := enFirstNames[rand.Intn(len(enFirstNames))]
	last := enLastNames[rand.Intn(len(enLastNames))]

	switch rand.Intn(5) {
	case 0: // sarah.miller92
		b.WriteString(first)
		b.WriteByte('.')
		b.WriteString(last)
		b.WriteString(strconv.Itoa(rand.Intn(100)))
	case 1: // michael_brown
		b.WriteString(first)
		b.WriteByte('_')
		b.WriteString(last)
	case 2: // s.miller
		b.WriteByte(first[0])
		b.WriteByte('.')
		b.WriteString(last)
	case 3: // sarah.miller
		b.WriteString(first)
		b.WriteByte('.')
		b.WriteString(last)
	default: // sarah.m
		b.WriteString(first)
		b.WriteByte('.')
		b.WriteByte(last[0])
	}
}

// ─── Thai patterns (90s Hotmail nostalgia + modern) ───

func genThai(b *strings.Builder) {
	nick := thaiNicknames[rand.Intn(len(thaiNicknames))]

	switch rand.Intn(10) {
	case 0: // toon_zaa
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(thaiSuffixZaa[rand.Intn(len(thaiSuffixZaa))])

	case 1: // ploy_narak
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(thaiSuffixCute[rand.Intn(len(thaiSuffixCute))])

	case 2: // bank_1990
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(strconv.Itoa(1985 + rand.Intn(25))) // ปี 1985-2009

	case 3: // arm_bkk
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(thaiSuffixCity[rand.Intn(len(thaiSuffixCity))])

	case 4: // boy_alone
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(thaiSuffixMood[rand.Intn(len(thaiSuffixMood))])

	case 5: // kitty_cute_90
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(thaiSuffixCute[rand.Intn(len(thaiSuffixCute))])
		b.WriteByte('_')
		b.WriteString(strconv.Itoa(rand.Intn(100)))

	case 6: // nong.somchai
		b.WriteString("nong.")
		b.WriteString(nick)

	case 7: // somchai.srisuk42 (Thai first.last+number)
		last := thaiLastNames[rand.Intn(len(thaiLastNames))]
		b.WriteString(nick)
		b.WriteByte('.')
		b.WriteString(last)
		b.WriteString(strconv.Itoa(rand.Intn(100)))

	case 8: // p.somchai / k.somchai (Thai honorific style)
		prefix := thaiHonorific[rand.Intn(len(thaiHonorific))]
		b.WriteString(prefix)
		b.WriteByte('.')
		b.WriteString(nick)

	default: // somchai_99
		b.WriteString(nick)
		b.WriteByte('_')
		b.WriteString(strconv.Itoa(rand.Intn(100)))
	}
}

// ─── EN Names ───

var enFirstNames = []string{
	"aaron", "adam", "alex", "andrew", "anthony", "austin", "ben", "blake",
	"brandon", "brian", "caleb", "cameron", "carlos", "carter", "charles", "charlie",
	"chris", "cole", "connor", "daniel", "david", "dean", "derek", "diego",
	"dylan", "edward", "eli", "eric", "ethan", "evan", "felix", "frank",
	"gabriel", "george", "grant", "harry", "henry", "hunter", "ian", "isaac",
	"jack", "jacob", "jake", "james", "jason", "jeff", "jeremy", "john",
	"jordan", "joseph", "josh", "julian", "justin", "kevin", "kyle", "liam",
	"logan", "lucas", "luke", "marcus", "mark", "mason", "matt", "max",
	"michael", "nathan", "nicholas", "nick", "noah", "oliver", "oscar", "owen",
	"patrick", "paul", "peter", "ryan", "sam", "samuel", "scott", "sean",
	"sebastian", "simon", "spencer", "stephen", "steve", "thomas", "timothy", "tony",
	"travis", "tyler", "victor", "vincent", "william", "wyatt", "zachary",
	"abigail", "alice", "amanda", "amber", "amelia", "amy", "angela", "anna",
	"ashley", "audrey", "ava", "bella", "beth", "brittany", "brooke", "camille",
	"carmen", "caroline", "charlotte", "chloe", "christina", "claire", "clara", "daisy",
	"diana", "elena", "elizabeth", "ella", "emily", "emma", "erica", "eva",
	"faith", "fiona", "grace", "hannah", "heather", "helen", "holly", "iris",
	"isabella", "ivy", "jade", "jasmine", "jennifer", "jessica", "julia", "julie",
	"karen", "kate", "katie", "kelly", "laura", "lauren", "lily", "linda",
	"lisa", "lucy", "madison", "maria", "maya", "megan", "melissa", "mia",
	"michelle", "molly", "morgan", "natalie", "nicole", "nina", "nora", "olivia",
	"paige", "rachel", "rebecca", "riley", "ruby", "samantha", "sara", "sarah",
	"savannah", "sophia", "stella", "stephanie", "taylor", "tiffany", "vanessa",
	"victoria", "violet", "vivian", "wendy", "zoe",
}

var enLastNames = []string{
	"adams", "allen", "anderson", "baker", "barnes", "bell", "bennett", "black",
	"brooks", "brown", "butler", "campbell", "carter", "chen", "clark", "cole",
	"collins", "cook", "cooper", "cox", "cruz", "davis", "diaz", "dixon",
	"edwards", "ellis", "evans", "fisher", "flores", "ford", "foster", "fox",
	"garcia", "gibson", "gomez", "gonzalez", "gordon", "graham", "gray", "green",
	"griffin", "hall", "hamilton", "harris", "harrison", "hart", "hayes", "henderson",
	"henry", "hernandez", "hill", "hoffman", "howard", "hudson", "hughes", "hunt",
	"jackson", "james", "jenkins", "johnson", "jones", "jordan", "kelly", "kennedy",
	"kim", "king", "knight", "lee", "lewis", "lin", "long", "lopez",
	"martin", "martinez", "mason", "miller", "mitchell", "moore", "morgan", "morris",
	"murphy", "murray", "nelson", "nguyen", "oliver", "ortiz", "owens", "parker",
	"patel", "perez", "perry", "peterson", "phillips", "powell", "price", "reed",
	"reyes", "reynolds", "richardson", "rivera", "roberts", "robinson", "rodriguez", "rogers",
	"ross", "russell", "sanchez", "sanders", "scott", "shaw", "silva", "simmons",
	"singh", "smith", "snyder", "spencer", "stevens", "stewart", "stone", "sullivan",
	"taylor", "thomas", "thompson", "torres", "tran", "turner", "walker", "wallace",
	"walsh", "wang", "ward", "watson", "webb", "wells", "west", "white",
	"williams", "wilson", "wong", "wood", "wright", "wu", "yang", "young",
}

// ─── Thai Nicknames (ชื่อเล่นไทย romanized) ───

var thaiNicknames = []string{
	// ยอดนิยมตลอดกาล
	"arm", "aom", "amp", "aun", "are",
	"bank", "ball", "bam", "bass", "beam", "beer", "bell", "ben", "best",
	"big", "bike", "bill", "blue", "boat", "bomb", "bon", "book", "boom",
	"boss", "bow", "boy", "bua", "bum",
	"cake", "can", "cap", "car", "cat", "cent", "chai", "cham", "champ",
	"chin", "chip", "chom", "cream",
	"dam", "dan", "dear", "dee", "dew", "din", "dome", "donut", "dream",
	"earn", "earth", "eak", "em", "eye",
	"fah", "fair", "fan", "fern", "film", "first", "fluke", "focus",
	"fon", "ford", "frame", "frank", "fruit",
	"gam", "gap", "gift", "gin", "ging", "golf", "green", "gus", "gun",
	"guy", "gig",
	"ice", "ink", "im",
	"jam", "jan", "jane", "jang", "jay", "jean", "jeed", "jet", "jim",
	"jib", "jit", "jo", "joe", "joy", "june", "jung",
	"kai", "karn", "kate", "kaew", "keng", "kim", "king", "kit", "kla",
	"kong", "korn", "krit", "kung", "kwang",
	"lee", "lek", "lin", "look", "luke",
	"maew", "mai", "man", "mam", "map", "mark", "may", "mew", "milk",
	"mind", "mint", "mod", "moo", "mook", "moon", "moss", "muay", "mund",
	"nai", "nam", "nan", "nana", "narm", "nat", "new", "nick", "nid",
	"nik", "ning", "nit", "noi", "non", "nong", "noom", "noon", "nor",
	"note", "nun", "nut",
	"oak", "oil", "om", "one", "ong", "oat", "ohm", "off",
	"pam", "pan", "pang", "pat", "peach", "peak", "pear", "pen", "pet",
	"pick", "pin", "ping", "ploy", "ploy", "pom", "pon", "pond",
	"pop", "por", "poy", "prae", "prim", "proud", "pui", "pun", "put",
	"rain", "rung",
	"sa", "sam", "sand", "sang", "sine", "som", "son", "star", "stamp",
	"tan", "tang", "tarn", "tee", "ten", "thai", "tle", "tob", "tom",
	"ton", "tong", "took", "toon", "top", "toy", "tui", "tum",
	"view", "vim",
	"wan", "war", "whan", "win", "wit",
	"yam", "yok", "yui",
	"zen", "zoom",
}

// ─── Thai Suffixes ───

var thaiSuffixZaa = []string{
	"zaa", "za", "zaaa", "za555", "zab", "zap",
	"ja", "jaa", "jaaa", "jung", "jung2",
	"na", "naa", "naja", "naka", "nako", "nakub", "na_ja",
	"ka", "kaa", "kab", "krub", "krab",
}

var thaiSuffixCute = []string{
	"narak", "cute", "sweet", "love", "lovely", "kawaii",
	"chujai", "rakdee", "ruk", "rak", "in_love",
	"happy", "smile", "angel", "honey", "cherry",
	"candy", "lucky", "pretty", "baby", "darling",
	"little", "tiny", "mini", "pink", "peach",
}

var thaiSuffixMood = []string{
	"alone", "chill", "cool", "crazy", "dream",
	"fly", "free", "fresh", "good", "great",
	"happy", "high", "hot", "king", "legend",
	"life", "live", "mad", "max", "mega",
	"nice", "okay", "one", "play", "pro",
	"real", "rich", "rock", "solo", "super",
	"the", "top", "true", "vip", "wow",
	"x", "yo", "zen", "zero", "zone",
	"hia", "suay", "lor", "jeed", "mak",
}

var thaiSuffixCity = []string{
	"bkk", "bk", "cn", "cm", "cnx", "hkt", "kkn", "kbi",
	"nkr", "pkn", "sby", "udn", "ubn", "ryn", "srn", "nan",
	"thai", "siam", "th",
}

var thaiHonorific = []string{
	"p", "k", "n", "nong", "phi", "ai",
}

var thaiLastNames = []string{
	"srisuk", "sritong", "sripan", "sriboon", "srisai", "srisawat",
	"thongdee", "thongsri", "thongkam", "thongyai",
	"wongsri", "wongdee", "wongsawat", "wongpan",
	"kaewsai", "kaewsuk", "kaewdee", "kaewpan",
	"jandee", "jansuk", "jantong",
	"meesuk", "meedee", "meeboon",
	"singha", "singharat",
	"rungruang", "rungsri", "rungdee",
	"tongdee", "tongkam", "tongsri",
	"polsri", "poldee", "polkaew",
	"buasri", "buadee", "buaphan",
	"yodsri", "yodkaew", "yodthong",
	"phimsri", "phimdee", "phimthong",
	"detsri", "detkaew", "dettong",
	"saetang", "saelim", "saetong",
	"chaiyasit", "chaiyawat",
}
