import XCTest
@testable import CloudHostKit

final class CloudDocumentsTests: XCTestCase {
  private let now = Date(timeIntervalSince1970: 1800000000)
  private func key(_ byte: UInt8 = 1) -> String {
    "ssh-ed25519 " + Data([0,0,0,11] + Array("ssh-ed25519".utf8) + [0,0,0,32] + Array(repeating: byte,count:32)).base64EncodedString()
  }
  private func host() -> CloudHost { CloudHost(id:UUID(),name:"我的 Mac",hostKey:key(),updatedAt:1800000000) }
  func testHostValidation() throws {
    try host().validate(now:now)
    XCTAssertThrowsError(try CloudHost(id:UUID(),name:"bad\nname",hostKey:key(),updatedAt:1800000000).validate(now:now))
    XCTAssertThrowsError(try CloudHost(id:UUID(),name:"Mac",hostKey:"ssh-ed25519 garbage",updatedAt:1800000000).validate(now:now))
    XCTAssertThrowsError(try CloudHost(id:UUID(),name:"Mac",hostKey:key(),updatedAt:1800009999).validate(now:now))
  }
  func testEnrollmentExpiryAndBinding() throws {
    let request = CloudEnrollment(host:host(),publicKey:key(2),now:now)
    try request.validate(now:now)
    XCTAssertThrowsError(try request.validate(now:now.addingTimeInterval(240)))
    XCTAssertThrowsError(try request.validate(now:now.addingTimeInterval(-61)))
    var reply = request; reply.invitation = "kagero://pair?data=example"
    XCTAssertTrue(reply.matches(request))
    let swapped = CloudEnrollment(id:request.id,host:host(),publicKey:key(2),now:now)
    XCTAssertFalse(swapped.matches(request))
    reply.invitation = String(repeating:"x",count:8193)
    XCTAssertThrowsError(try reply.validate(now:now))
  }
  func testRoundTripContainsNoPrivateKey() throws {
    let request = CloudEnrollment(host:host(),publicKey:key(2),now:now)
    let data = try JSONEncoder().encode(request)
    XCTAssertEqual(try JSONDecoder().decode(CloudEnrollment.self,from:data),request)
    let object = try XCTUnwrap(JSONSerialization.jsonObject(with:data) as? [String:Any])
    XCTAssertEqual(Set(object.keys),["id","hostID","hostKey","publicKey","expiresAt"])
  }
}
