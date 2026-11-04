//! The village calendar: seasons, flood days and what each place yields.
//!
//! Everything here is a pure function of the day number and the world seed,
//! so the whole year can be printed in advance (the panchang) and tested.

/// Game days in one season.
pub const SEASON_DAYS: u32 = 7;
/// Energy a player gets each game day. Gathering costs one.
pub const ENERGY_PER_DAY: u32 = 10;
/// Chance, in percent, that a monsoon day floods the road to the city.
pub const FLOOD_CHANCE_PCT: u64 = 45;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Season {
    /// Summer: mangoes everywhere.
    Garmi,
    /// Monsoon: fish plenty, chillies rot, the road floods.
    Barsaat,
    /// Festival season: rice harvest, the city wants pickle for Diwali.
    Tyohar,
}

impl Season {
    pub fn name(self) -> &'static str {
        match self {
            Season::Garmi => "garmi",
            Season::Barsaat => "barsaat",
            Season::Tyohar => "tyohar",
        }
    }
}

/// The season for a game day. The year is 21 days long and starts in summer.
pub fn season_of(day: u32) -> Season {
    match (day / SEASON_DAYS) % 3 {
        0 => Season::Garmi,
        1 => Season::Barsaat,
        _ => Season::Tyohar,
    }
}

/// A small, well-mixed deterministic hash (splitmix64). Good enough for a game
/// calendar; it is not used for anything that needs to be unpredictable.
pub fn mix(mut x: u64) -> u64 {
    x = x.wrapping_add(0x9E37_79B9_7F4A_7C15);
    x = (x ^ (x >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
    x = (x ^ (x >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
    x ^ (x >> 31)
}

/// True when the road to the city is flooded on this day.
pub fn is_flood(seed: u64, day: u32) -> bool {
    season_of(day) == Season::Barsaat && mix(seed ^ ((day as u64) << 8)) % 100 < FLOOD_CHANCE_PCT
}

/// The gathering places and the item each one gives.
pub const PLACES: [(&str, &str); 4] = [
    ("grove", "mango"),
    ("field", "rice"),
    ("river", "fish"),
    ("garden", "chilli"),
];

/// Item a place gives, if the place exists.
pub fn place_item(place: &str) -> Option<&'static str> {
    PLACES
        .iter()
        .find(|(p, _)| *p == place)
        .map(|(_, item)| *item)
}

/// Base yield for one gathering action, by season.
pub fn base_yield(item: &str, season: Season) -> u32 {
    let table: [u32; 4] = match season {
        //              mango rice fish chilli
        Season::Garmi => [3, 1, 1, 2],
        Season::Barsaat => [0, 1, 3, 0],
        Season::Tyohar => [1, 3, 2, 2],
    };
    match item {
        "mango" => table[0],
        "rice" => table[1],
        "fish" => table[2],
        "chilli" => table[3],
        _ => 0,
    }
}

/// Yield for one gathering action. A lucky extra unit appears about half the
/// time, but never in a season when the place gives nothing.
pub fn gather_yield(item: &str, season: Season, luck: u64) -> u32 {
    let base = base_yield(item, season);
    if base == 0 {
        0
    } else {
        base + (luck % 2) as u32
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn seasons_cycle_every_21_days() {
        assert_eq!(season_of(0), Season::Garmi);
        assert_eq!(season_of(6), Season::Garmi);
        assert_eq!(season_of(7), Season::Barsaat);
        assert_eq!(season_of(14), Season::Tyohar);
        assert_eq!(season_of(21), Season::Garmi);
    }

    #[test]
    fn floods_only_in_monsoon_and_some_days() {
        let seed = 42;
        for day in 0..63 {
            if season_of(day) != Season::Barsaat {
                assert!(!is_flood(seed, day));
            }
        }
        let monsoon_floods = (7..14).filter(|d| is_flood(seed, *d)).count()
            + (28..35).filter(|d| is_flood(seed, *d)).count()
            + (49..56).filter(|d| is_flood(seed, *d)).count();
        assert!(
            monsoon_floods > 0 && monsoon_floods < 21,
            "got {monsoon_floods}"
        );
    }

    #[test]
    fn monsoon_has_no_mangoes() {
        for luck in 0..4 {
            assert_eq!(gather_yield("mango", Season::Barsaat, luck), 0);
        }
        assert!(gather_yield("fish", Season::Barsaat, 0) >= 3);
    }

    #[test]
    fn places_map_to_items() {
        assert_eq!(place_item("grove"), Some("mango"));
        assert_eq!(place_item("river"), Some("fish"));
        assert_eq!(place_item("moon"), None);
    }
}
